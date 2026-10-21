package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/operation"
	"github.com/DeliciousBuding/chaoxing/internal/upstream"
)

var errPartial = errors.New("partial result")
var errHelp = errors.New("help displayed")

func usage() error {
	fmt.Fprintln(os.Stdout, "chaoxing version | configure | serve | call | query | capabilities | schema <operation> | location | courses | watch\nBusiness commands use the running local daemon; --json is accepted (JSON is the default).")
	fmt.Fprintln(os.Stdout, locationUsage())
	fmt.Fprintln(os.Stdout, "watch classes  list | add --course <id> --class <id> --days Tue,Thu --start 08:00 --end 09:50 | remove ...")
	for _, d := range operation.All() {
		fmt.Fprintf(os.Stdout, "  %s: %s\n", strings.ReplaceAll(d.ID, ".", " "), d.Description)
	}
	return nil
}
func parseBusiness(argv []string) (request, bool, error) {
	if len(argv) == 0 {
		return request{}, false, nil
	}
	name := argv[0]
	start := 1
	if len(argv) > 1 && !strings.HasPrefix(argv[1], "--") {
		candidate := name + "." + argv[1]
		if _, ok := operation.Find(candidate); ok {
			name = candidate
			start = 2
		}
	}
	d, ok := operation.Find(name)
	if !ok {
		return request{}, false, nil
	}
	r := request{Tool: d.Name, Arguments: map[string]any{}}
	for i := start; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--json" {
			continue
		}
		if arg == "--help" {
			b, _ := json.MarshalIndent(d.InputSchema(), "", "  ")
			fmt.Fprintln(os.Stdout, string(b))
			return request{}, true, errHelp
		}
		if !strings.HasPrefix(arg, "--") {
			if _, ok := d.Fields["id"]; ok {
				if _, used := r.Arguments["id"]; !used {
					r.Arguments["id"] = arg
					continue
				}
			}
			return r, true, &upstream.Fault{Code: "INVALID_ARGUMENT"}
		}
		key := strings.ReplaceAll(strings.TrimPrefix(arg, "--"), "-", "_")
		f, ok := d.Fields[key]
		if !ok || i+1 >= len(argv) {
			return r, true, &upstream.Fault{Code: "INVALID_ARGUMENT"}
		}
		i++
		if _, exists := r.Arguments[key]; exists {
			return r, true, &upstream.Fault{Code: "INVALID_ARGUMENT"}
		}
		if f.Type == "integer" || f.Type == "number" {
			r.Arguments[key] = json.Number(argv[i])
		} else {
			r.Arguments[key] = argv[i]
		}
	}
	if operation.Validate(d, r.Arguments) != nil {
		return r, true, &upstream.Fault{Code: "INVALID_ARGUMENT"}
	}
	return r, true, nil
}
func emitResult(name string, v any) error {
	out, e := operation.Result(name, v)
	if e != nil {
		return e
	}
	if e = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "data": out}); e != nil {
		return e
	}
	if out["meta"].(map[string]any)["status"] == "partial" {
		return errPartial
	}
	return nil
}
func callDaemon(r request) error {
	d, ok := operation.Find(r.Tool)
	if !ok {
		return &upstream.Fault{Code: "INVALID_TOOL"}
	}
	if operation.Validate(d, r.Arguments) != nil {
		return &upstream.Fault{Code: "INVALID_ARGUMENT"}
	}
	r.Tool = d.Name
	c, e := load()
	if e != nil {
		return e
	}
	if !loopback(c.LocalListen) {
		return errors.New("invalid local listener")
	}
	b, _ := json.Marshal(r)
	req, e := http.NewRequest("POST", "http://"+c.LocalListen+"/call", strings.NewReader(string(b)))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+c.LocalToken)
	h := http.Client{Timeout: 100 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := h.Do(req)
	if e != nil {
		return &upstream.Fault{Code: "UPSTREAM_UNAVAILABLE"}
	}
	defer resp.Body.Close()
	b, e = io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if e != nil || len(b) > 2<<20 {
		return &upstream.Fault{Code: "RESPONSE_TOO_LARGE"}
	}
	return emitDaemonResponse(d.Name, resp.StatusCode, b)
}

func emitDaemonResponse(name string, status int, b []byte) error {
	var envelope struct {
		OK    bool            `json:"ok"`
		Data  any             `json:"data"`
		Error *upstream.Fault `json:"error"`
	}
	if json.Unmarshal(b, &envelope) != nil {
		switch status {
		case http.StatusTooManyRequests:
			return &upstream.Fault{Code: "RATE_LIMITED"}
		case http.StatusUnauthorized, http.StatusForbidden:
			return &upstream.Fault{Code: "AUTH_REQUIRED"}
		}
		if status != http.StatusOK {
			return &upstream.Fault{Code: "UPSTREAM_UNAVAILABLE"}
		}
		return errors.New("invalid daemon response")
	}
	if status != http.StatusOK || !envelope.OK {
		if envelope.Error != nil {
			return envelope.Error
		}
		return &upstream.Fault{Code: "UPSTREAM_UNAVAILABLE"}
	}
	return emitResult(name, envelope.Data)
}
