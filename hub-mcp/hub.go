package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// hubClient 用 Agent Hub 的 remote token 呼叫 API。
//
// remote token 本身就被 Hub 限制住：不能部署、不能碰 Devices、不能 merge、不能核准主機上的指令，
// 開的任務一律是 Codex、不帶 Devices。這裡再收窄一次：只開放下面這幾個動作，
// repo 只能用別名選，任務 ID 只能是 Hub 給的格式。
type hubClient struct {
	base, token string
	client      *http.Client
}

func newHubClient(base, token string) *hubClient {
	return &hubClient{base: strings.TrimRight(base, "/"), token: token, client: &http.Client{Timeout: 40 * time.Second}}
}

type hubError struct {
	Status int
	Msg    string
}

func (e *hubError) Error() string { return fmt.Sprintf("Agent Hub 回 HTTP %d：%s", e.Status, e.Msg) }

func (h *hubClient) call(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		if json.Unmarshal(raw, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(raw))
		}
		return &hubError{Status: resp.StatusCode, Msg: e.Error}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}

type task struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Workflow  string  `json:"workflow"`
	Status    string  `json:"status"`
	Activity  *string `json:"activity"`
	UpdatedAt string  `json:"updatedAt"`
	Workspace *struct {
		PR *struct {
			Number int    `json:"number"`
			URL    string `json:"url"`
			State  string `json:"state"`
		} `json:"pr"`
	} `json:"workspace"`
}

type summary struct {
	TaskID  string `json:"taskId"`
	Project *struct {
		Name string `json:"name"`
	} `json:"project"`
	PendingAsk         bool `json:"pendingAsk"`
	AwaitingAcceptance bool `json:"awaitingAcceptance"`
}

type taskList struct {
	Tasks     []task    `json:"tasks"`
	Summaries []summary `json:"summaries"`
}

// event 只解出用得到的欄位；其餘留在 Raw，開 PR 時要原樣帶回去。
type event struct {
	ID          string          `json:"id"`
	Seq         int             `json:"seq"`
	Type        string          `json:"type"`
	Text        string          `json:"text"`
	Question    string          `json:"question"`
	Context     string          `json:"context"`
	Recommended string          `json:"recommended"`
	Options     []askOption     `json:"options"`
	Answer      string          `json:"answer"`
	Superseded  bool            `json:"superseded"`
	OK          *bool           `json:"ok"`
	Error       string          `json:"error"`
	Branch      string          `json:"branch"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	CommitMsg   *string         `json:"commitMessage"`
	Purpose     string          `json:"purpose"`
	Raw         json.RawMessage `json:"-"`
}

type askOption struct {
	Label  string `json:"label"`
	Detail string `json:"detail"`
}

func (h *hubClient) list(ctx context.Context) (taskList, error) {
	var l taskList
	return l, h.call(ctx, http.MethodGet, "/api/task-list", nil, &l)
}

func (h *hubClient) task(ctx context.Context, id string) (task, error) {
	var t task
	return t, h.call(ctx, http.MethodGet, "/api/tasks/"+url.PathEscape(id), nil, &t)
}

func (h *hubClient) events(ctx context.Context, id string) ([]event, error) {
	var evs []event
	return evs, h.call(ctx, http.MethodGet, "/api/tasks/"+url.PathEscape(id)+"/events", nil, &evs)
}
