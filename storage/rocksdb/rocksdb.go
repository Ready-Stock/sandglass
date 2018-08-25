// +build cgo

package rocksdb

import (
	"C"

	"github.com/sandglass/sandglass/sgutils"
	"github.com/sandglass/sandglass/storage"
	"github.com/sandglass/sandglass/storage/scommons"
	"github.com/tecbot/gorocksdb"
)

var bbto *gorocksdb.BlockBasedTableOptions

func init() {
	bbto = gorocksdb.NewDefaultBlockBasedTableOptions()
	bbto.SetBlockCache(gorocksdb.NewLRUCache(3 << 30))
	bbto.SetBlockCacheCompressed(gorocksdb.NewLRUCache(3 << 30))
	filter := gorocksdb.NewBloomFilter(10)
	bbto.SetFilterPolicy(filter)
}

type Store struct {
	db *gorocksdb.DB
	scommons.StorageCommons
}

func NewStorage(path string, operators ...*storage.MergeOperator) (*Store, error) {
	opts := gorocksdb.NewDefaultOptions()
	opts.SetCreateIfMissing(true)
	opts.SetBlockBasedTableFactory(bbto)
	for _, operator := range operators {
		opts.SetMergeOperator(mergeOperator{op: operator, name: string(operator.Key)})
	}

	db, err := gorocksdb.OpenDb(opts, path)
	if err != nil {
		return nil, err
	}
	s := &Store{
		db: db,
	}
	s.StorageCommons = scommons.StorageCommons{s}
	return s, nil
}

func (s *Store) Get(key []byte) ([]byte, error) {
	opts := gorocksdb.NewDefaultReadOptions()
	slice, err := s.db.Get(opts, key)
	if err != nil {
		return nil, err
	}
	defer slice.Free()

	data := slice.Data()
	if data == nil {
		return nil, nil
	}

	return sgutils.CopyBytes(data), nil
}

func (s *Store) Put(key []byte, val []byte) error {
	opts := gorocksdb.NewDefaultWriteOptions()
	defer opts.Destroy()
	return s.db.Put(opts, key, val)
}

func (s *Store) BatchPut(entries []*storage.Entry) error {
	batch := gorocksdb.NewWriteBatch()
	defer batch.Destroy() // maybe use wb.Clear for reuse
	for _, e := range entries {
		batch.Put(e.Key, e.Value)
	}

	wopts := gorocksdb.NewDefaultWriteOptions()
	defer wopts.Destroy()

	return s.db.Write(wopts, batch)
}

func (s *Store) Merge(key, operation []byte) error {
	wopts := gorocksdb.NewDefaultWriteOptions()
	defer wopts.Destroy()

	return s.db.Merge(wopts, key, operation)
}

func (s *Store) ProcessMergedKey(key []byte, fn func(val []byte) ([]*storage.Entry, []byte, error)) error {
	batch := gorocksdb.NewWriteBatch()
	defer batch.Destroy() // maybe use wb.Clear for reuse

	wopts := gorocksdb.NewDefaultWriteOptions()
	defer wopts.Destroy()

	val, err := s.Get(key)
	if err != nil {
		return err
	}

	entries, operation, err := fn(val)
	for _, e := range entries {
		batch.Put(e.Key, e.Value)
	}

	batch.Merge(key, operation)

	return s.db.Write(wopts, batch)
}

func (s *Store) Iter(opts *storage.IterOptions) storage.Iterator {
	ropts := gorocksdb.NewDefaultReadOptions()
	defer ropts.Destroy()
	it := s.db.NewIterator(ropts)

	return &iterator{iter: it, opts: opts}
}

func (s *Store) Truncate(prefix, min []byte, batchSize int) error {
	truncate := func() (bool, error) {
		batch := gorocksdb.NewWriteBatch()
		defer batch.Destroy()

		ropts := gorocksdb.NewDefaultReadOptions()
		defer ropts.Destroy()

		it := s.db.NewIterator(ropts)

		buf := make([][]byte, 0, batchSize)

		for it.Seek(min); it.ValidForPrefix(prefix) && len(buf) < batchSize; it.Next() {
			slice := it.Key()
			key := sgutils.CopyBytes(slice.Data())
			buf = append(buf, key)
			slice.Free()
		}

		if len(buf) == 0 {
			return false, nil
		}

		for _, key := range buf {
			batch.Delete(key)
		}

		wopts := gorocksdb.NewDefaultWriteOptions()
		defer wopts.Destroy()

		if err := s.db.Write(wopts, batch); err != nil {
			return false, err
		}

		return true, nil
	}

	ok, err := truncate()
	for ; ok; ok, err = truncate() {
	}

	if err != nil {
		return err
	}

	return nil
}

func (s *Store) Delete(key []byte) error {
	wopts := gorocksdb.NewDefaultWriteOptions()
	defer wopts.Destroy()
	return s.db.Delete(wopts, key)
}

func (s *Store) BatchDelete(keys [][]byte) error {
	batch := gorocksdb.NewWriteBatch()
	defer batch.Destroy()

	for _, key := range keys {
		batch.Delete(key)
	}

	wopts := gorocksdb.NewDefaultWriteOptions()
	defer wopts.Destroy()

	return s.db.Write(wopts, batch)
}

func (s *Store) Close() error {
	s.db.Close()
	return nil
}

var _ storage.Storage = (*Store)(nil)

type mergeOperator struct {
	op   *storage.MergeOperator
	name string
}

func (mo mergeOperator) FullMerge(key, existingValue []byte, operands [][]byte) ([]byte, bool) {
	var ok bool
	for _, operand := range operands {
		existingValue, ok = mo.op.MergeFunc(existingValue, operand)
		if !ok {
			return nil, false
		}
	}

	return existingValue, true
}

func (mo mergeOperator) PartialMerge(key, leftOperand, rightOperand []byte) ([]byte, bool) {
	return nil, false
}

func (mo mergeOperator) Name() string { return "merge_operator" }
