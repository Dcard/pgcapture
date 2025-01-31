package pgcapture

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgtype"
	"github.com/rueian/pgcapture/pkg/pb"
	"github.com/rueian/pgcapture/pkg/source"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestConsumer(t *testing.T) {
	testBounceInterval(t, 0)
	testBounceInterval(t, time.Millisecond)
}

func TestConsumerRequeue(t *testing.T) {
	testBounceIntervalRequeue(t, 0)
	testBounceIntervalRequeue(t, time.Millisecond)
}

func testBounceInterval(t *testing.T, interval time.Duration) {
	ctx := context.Background()
	mock := &gatewayClient{
		sendQ: make(chan *pb.CaptureRequest),
		recvQ: make(chan *pb.CaptureMessage),
	}
	consumer := newConsumer(ctx, mock, ConsumerOption{
		URI:              "uri",
		TableRegex:       "regex",
		DebounceInterval: interval,
		OnDecodeError: func(source source.Change, err error) {
		},
	})
	defer consumer.Stop()
	decodeQ := make(chan Change)
	exit := make(chan error)
	go func() {
		exit <- consumer.Consume(map[Model]ModelHandlerFunc{
			&Model1{}: func(change Change) error {
				decodeQ <- change
				return nil
			},
			&Model2{}: func(change Change) error {
				decodeQ <- change
				return nil
			},
		})
	}()

	if init := <-mock.sendQ; !proto.Equal(init, &pb.CaptureRequest{Type: &pb.CaptureRequest_Init{Init: &pb.CaptureInit{
		Uri:        "uri",
		Parameters: &structpb.Struct{Fields: map[string]*structpb.Value{TableRegexOption: structpb.NewStringValue("regex")}},
	}}}) {
		t.Fatalf("unexpected init msg %v", init.String())
	}

	mock.recvQ <- &pb.CaptureMessage{
		Checkpoint: &pb.Checkpoint{Lsn: 1},
		Change: &pb.Change{
			Op:     pb.Change_INSERT,
			Schema: "public",
			Table:  "m1",
			New: []*pb.Field{
				{Name: "f1", Oid: pgtype.TextOID, Value: &pb.Field_Binary{Binary: []byte("f1")}},
				{Name: "f2", Oid: pgtype.TextOID, Value: nil},
			},
		},
	}

	if decoded := <-decodeQ; decoded.Op != pb.Change_INSERT ||
		decoded.Checkpoint.LSN != 1 ||
		decoded.New.(*Model1).F1.Get() != "f1" ||
		decoded.New.(*Model1).F2.Get() != nil ||
		decoded.New.(*Model1).F3.Get() != pgtype.Undefined {
		t.Fatalf("unexpected decoded %v", decoded)
	}

	if commit := <-mock.sendQ; !proto.Equal(commit, &pb.CaptureRequest{Type: &pb.CaptureRequest_Ack{Ack: &pb.CaptureAck{
		Checkpoint: &pb.Checkpoint{Lsn: 1},
	}}}) {
		t.Fatalf("unexpected commit msg %v", commit.String())
	}

	mock.recvQ <- &pb.CaptureMessage{
		Checkpoint: &pb.Checkpoint{Lsn: 2},
		Change: &pb.Change{
			Op:     pb.Change_UPDATE,
			Schema: "public",
			Table:  "m2",
			New: []*pb.Field{
				{Name: "f1", Oid: pgtype.TextOID, Value: &pb.Field_Binary{Binary: []byte("f1")}},
			},
		},
	}

	if decoded := <-decodeQ; decoded.Op != pb.Change_UPDATE || decoded.Checkpoint.LSN != 2 || decoded.New.(*Model2).F1.Get() != "f1" {
		t.Fatalf("unexpected decoded %v", decoded)
	}

	if commit := <-mock.sendQ; !proto.Equal(commit, &pb.CaptureRequest{Type: &pb.CaptureRequest_Ack{Ack: &pb.CaptureAck{
		Checkpoint: &pb.Checkpoint{Lsn: 2},
	}}}) {
		t.Fatalf("unexpected commit msg %v", commit.String())
	}

	mock.recvQ <- &pb.CaptureMessage{
		Checkpoint: &pb.Checkpoint{Lsn: 3},
		Change: &pb.Change{
			Op:     pb.Change_DELETE,
			Schema: "public",
			Table:  "m2",
			Old: []*pb.Field{
				{Name: "f1", Oid: pgtype.TextOID, Value: &pb.Field_Text{Text: "f1"}},
			},
		},
	}

	if decoded := <-decodeQ; decoded.Op != pb.Change_DELETE || decoded.Checkpoint.LSN != 3 || decoded.Old.(*Model2).F1.Get() != "f1" {
		t.Fatalf("unexpected decoded %v", decoded)
	}

	if commit := <-mock.sendQ; !proto.Equal(commit, &pb.CaptureRequest{Type: &pb.CaptureRequest_Ack{Ack: &pb.CaptureAck{
		Checkpoint: &pb.Checkpoint{Lsn: 3},
	}}}) {
		t.Fatalf("unexpected commit msg %v", commit.String())
	}

	close(mock.sendQ)
	close(mock.recvQ)

	if err := <-exit; err.Error() != "closed" {
		t.Fatalf("unexpected exit error %v", err)
	}
}

func testBounceIntervalRequeue(t *testing.T, interval time.Duration) {
	ctx := context.Background()
	mock := &gatewayClient{
		sendQ: make(chan *pb.CaptureRequest),
		recvQ: make(chan *pb.CaptureMessage),
	}
	consumer := newConsumer(ctx, mock, ConsumerOption{
		URI:              "uri",
		TableRegex:       "regex",
		DebounceInterval: interval,
		OnDecodeError: func(source source.Change, err error) {
		},
	})
	defer consumer.Stop()

	exit := make(chan error)
	go func() {
		exit <- consumer.Consume(map[Model]ModelHandlerFunc{
			&Model1{}: func(change Change) error {
				return errors.New("err")
			},
		})
	}()

	if init := <-mock.sendQ; !proto.Equal(init, &pb.CaptureRequest{Type: &pb.CaptureRequest_Init{Init: &pb.CaptureInit{
		Uri:        "uri",
		Parameters: &structpb.Struct{Fields: map[string]*structpb.Value{TableRegexOption: structpb.NewStringValue("regex")}},
	}}}) {
		t.Fatalf("unexpected init msg %v", init.String())
	}

	mock.recvQ <- &pb.CaptureMessage{
		Checkpoint: &pb.Checkpoint{Lsn: 1},
		Change:     &pb.Change{Op: pb.Change_INSERT, Schema: "public", Table: "m1", New: []*pb.Field{}},
	}

	if requeue := <-mock.sendQ; !proto.Equal(requeue, &pb.CaptureRequest{Type: &pb.CaptureRequest_Ack{Ack: &pb.CaptureAck{
		Checkpoint: &pb.Checkpoint{Lsn: 1}, RequeueReason: "err",
	}}}) {
		t.Fatalf("unexpected requeue msg %v", requeue.String())
	}

	close(mock.sendQ)
	close(mock.recvQ)

	if err := <-exit; err.Error() != "closed" {
		t.Fatalf("unexpected exit error %v", err)
	}
}

type Model1 struct {
	F1 pgtype.Text `pg:"f1"`
	F2 pgtype.Text `pg:"f2"`
	F3 pgtype.Text `pg:"f3"`
}

func (m *Model1) DebounceKey() string {
	return "1"
}

func (m *Model1) TableName() (schema, table string) {
	return "public", "m1"
}

type Model2 struct {
	F1 pgtype.Text `pg:"f1"`
}

func (m *Model2) DebounceKey() string {
	return "2"
}

func (m *Model2) TableName() (schema, table string) {
	return "", "m2"
}

type gatewayClient struct {
	sendQ chan *pb.CaptureRequest
	recvQ chan *pb.CaptureMessage
}

func (g *gatewayClient) Send(request *pb.CaptureRequest) error {
	g.sendQ <- request
	return nil
}

func (g *gatewayClient) Recv() (*pb.CaptureMessage, error) {
	if msg, ok := <-g.recvQ; ok {
		return msg, nil
	}
	return nil, errors.New("closed")
}

func (g *gatewayClient) Header() (metadata.MD, error) {
	panic("implement me")
}

func (g *gatewayClient) Trailer() metadata.MD {
	panic("implement me")
}

func (g *gatewayClient) CloseSend() error {
	panic("implement me")
}

func (g *gatewayClient) Context() context.Context {
	panic("implement me")
}

func (g *gatewayClient) SendMsg(m interface{}) error {
	panic("implement me")
}

func (g *gatewayClient) RecvMsg(m interface{}) error {
	panic("implement me")
}

func (g *gatewayClient) Capture(ctx context.Context, opts ...grpc.CallOption) (pb.DBLogGateway_CaptureClient, error) {
	return g, nil
}
