package routing

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithCallDeadline_ZeroDisables(t *testing.T) {
	ctx, cancel := WithCallDeadline(context.Background(), 0)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("zero timeout should not set a deadline")
	}
}

func TestContextStopError_NamesDeadline(t *testing.T) {
	ctx, cancel := WithCallDeadline(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()
	if !CallDeadlineHit(ctx) {
		t.Fatal("CallDeadlineHit = false")
	}
	err := ContextStopError(ctx, "search", []error{errors.New("a"), errors.New("b")})
	if !errors.Is(err, ErrCallDeadline) {
		t.Fatalf("err %v does not wrap ErrCallDeadline", err)
	}
	if err := ContextStopError(ctx, "search", nil); !errors.Is(err, ErrCallDeadline) {
		t.Fatalf("no-attempt err %v does not wrap ErrCallDeadline", err)
	}
}

func TestContextStopError_ClientCancelIsNotDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if CallDeadlineHit(ctx) {
		t.Fatal("client cancel misreported as call deadline")
	}
	if err := ContextStopError(ctx, "extract", nil); errors.Is(err, ErrCallDeadline) {
		t.Fatalf("err %v should not wrap ErrCallDeadline", err)
	}
}
