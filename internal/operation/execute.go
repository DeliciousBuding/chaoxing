package operation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"time"
)

type Handler func(context.Context, string, map[string]any) (any, error)

// Execute adds bounded, content-free request observability around the shared contract.
func Execute(ctx context.Context, name string, args map[string]any, handler Handler) (any, error) {
	d, ok := Find(name)
	if !ok {
		return nil, ErrUnknownOperation
	}
	if e := Validate(d, args); e != nil {
		return nil, ErrInvalidArgument
	}
	started := time.Now()
	traceBytes := make([]byte, 12)
	if _, e := rand.Read(traceBytes); e != nil {
		return nil, e
	}
	trace := hex.EncodeToString(traceBytes)
	status := "error"
	defer func() {
		log.Printf("operation=%s trace_id=%s status=%s duration_ms=%d", d.ID, trace, status, time.Since(started).Milliseconds())
	}()
	v, e := handler(ctx, d.Name, args)
	if e != nil {
		return nil, e
	}
	out, e := Result(d.Name, v)
	if e != nil {
		return nil, e
	}
	meta := out["meta"].(map[string]any)
	meta["trace_id"] = trace
	status = meta["status"].(string)
	return out, nil
}
