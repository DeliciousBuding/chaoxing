package upstream

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

const workSubmitAPI = "https://mooc1.chaoxing.com/mooc-ans/work/addStudentWorkNew"

type WorkSubmissionResult struct {
	JobID     string    `json:"job_id"`
	Action    string    `json:"action"`
	State     string    `json:"state"`
	Verified  bool      `json:"verified"`
	Message   string    `json:"message"`
	FetchedAt time.Time `json:"fetched_at"`
}

func (c *Client) SubmitWorkPayload(ctx context.Context, action string, payload map[string]string) (WorkSubmissionResult, error) {
	if action != "save" && action != "submit" {
		return WorkSubmissionResult{}, fault("INVALID_ARGUMENT", "work action must be save or submit")
	}
	if len(payload) == 0 || len(payload) > 512 {
		return WorkSubmissionResult{}, fault("INVALID_ARGUMENT", "work payload is empty or exceeds bounds")
	}
	form := make(url.Values, len(payload))
	for key, value := range payload {
		if strings.TrimSpace(key) == "" || len(key) > 128 || len(value) > 16384 {
			return WorkSubmissionResult{}, fault("INVALID_ARGUMENT", "work payload field is invalid")
		}
		form.Set(key, value)
	}
	form.Set("pyFlag", map[string]string{"save": "1", "submit": ""}[action])
	if err := c.lock(ctx); err != nil {
		return WorkSubmissionResult{}, err
	}
	defer c.queries.Release()
	b, _, err := c.rawWrite(ctx, "POST", workSubmitAPI, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return WorkSubmissionResult{}, err
	}
	var payloadResponse struct {
		Status bool   `json:"status"`
		Msg    string `json:"msg"`
	}
	if json.Unmarshal(b, &payloadResponse) != nil {
		return WorkSubmissionResult{}, fault("UPSTREAM_SCHEMA", "work submission response not recognized")
	}
	if !payloadResponse.Status {
		message := strings.TrimSpace(payloadResponse.Msg)
		if strings.Contains(message, "过期") {
			return WorkSubmissionResult{}, fault("WORK_UNAVAILABLE", "work has ended")
		}
		return WorkSubmissionResult{}, fault("WORK_SUBMIT_REJECTED", "upstream rejected the work payload")
	}
	jobID := firstNonBlank(payload["jobid"], payload["jobId"])
	return WorkSubmissionResult{JobID: jobID, Action: action, State: "accepted", Verified: true, Message: strings.TrimSpace(payloadResponse.Msg), FetchedAt: time.Now().UTC()}, nil
}
