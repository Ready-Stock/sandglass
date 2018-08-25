package broker

import (
	"context"
	"errors"
	"fmt"

	"github.com/sandglass/sandglass/topic"
	"github.com/sirupsen/logrus"

	"github.com/sandglass/sandglass-grpc/go/sgproto"
)

var (
	ErrNoKeySet           = errors.New("ErrNoKeySet")
	ErrNoMessageToProduce = errors.New("ErrNoMessageToProduce")
)

func (b *Broker) Produce(ctx context.Context, req *sgproto.ProduceMessageRequest) (*sgproto.ProduceResponse, error) {
	b.WithFields(logrus.Fields{
		"topic":     req.Topic,
		"partition": req.Partition,
		"messages":  len(req.Messages),
	}).Debugf("produce message")
	if len(req.Messages) == 0 {
		return nil, ErrNoMessageToProduce
	}

	t := b.getTopic(req.Topic)
	if t == nil {
		return nil, ErrTopicNotFound
	}

	var p *topic.Partition
	if req.Partition != "" { // already specified
		if p = t.GetPartition(req.Partition); p == nil {
			return nil, fmt.Errorf("unknown partition '%s'", req.Partition)
		}
	} else { // choose one
		p = t.ChooseRandomPartition()
	}

	leader := b.getPartitionLeader(req.Topic, p.Id)
	if leader == nil {
		return nil, ErrNoLeaderFound
	}

	if leader.Name != b.Name() {
		return leader.Produce(ctx, req)
	}

	err := p.BatchPutMessages(req.Messages)
	if err != nil {
		return nil, err
	}

	res := &sgproto.ProduceResponse{}
	for _, msg := range req.Messages {
		res.Offsets = append(res.Offsets, msg.Offset)
	}

	return res, nil
}
