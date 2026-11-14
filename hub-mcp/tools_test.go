package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	idAsk  = "11111111-aaaa-4aaa-8aaa-000000000001"
	idDone = "22222222-bbbb-4bbb-8bbb-000000000002"
)

type fakeHub struct {
	t        *testing.T
	posts    []string
	bodies   []map[string]any
	draftID  string
	forbidPR bool
}

func (f *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer remote" {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	if r.Method == http.MethodPost {
		f.posts = append(f.posts, r.URL.Path)
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		f.bodies = append(f.bodies, b)
	}
	switch {
	case r.URL.Path == "/api/task-list":
		fmt.Fprintf(w, `{"tasks":[{"id":%q,"title":"Port PR base","workflow":"plan","status":"idle","activity":"Waiting for you","updatedAt":"2026-11-11"},
			{"id":%q,"title":"Old","workflow":"chat","status":"done","activity":null,"updatedAt":"2026-11-01"}],
			"summaries":[{"taskId":%q,"project":{"name":"agent-hub"},"pendingAsk":true,"awaitingAcceptance":false}]}`, idAsk, idDone, idAsk)
	case r.URL.Path == "/api/tasks/"+idAsk && r.Method == http.MethodGet:
		fmt.Fprintf(w, `{"id":%q,"title":"Port PR base","workflow":"plan","status":"idle","activity":"Waiting for you"}`, idAsk)
	case r.URL.Path == "/api/tasks/"+idAsk+"/events":
		fmt.Fprintf(w, `[{"id":"e1","seq":1,"type":"agent_text","text":"I checked main."},
			{"id":"e2","seq":2,"type":"ask","question":"Start porting now?","options":[{"label":"A","detail":"port"}],"recommended":"A"},
			{"id":%q,"seq":3,"type":"pr_draft","title":"feat: port","branch":"agent-hub/x","body":"Body","commitMessage":"feat: port"}]`, f.draftID)
	case r.URL.Path == "/api/tasks/"+idAsk+"/pr/draft":
		fmt.Fprintf(w, `{"id":%q,"type":"pr_draft","title":"feat: port","branch":"agent-hub/x","body":"Body"}`, f.draftID)
	case r.URL.Path == "/api/tasks/"+idAsk+"/pr":
		if f.forbidPR {
			http.Error(w, `{"error":"not allowed for the remote token"}`, 403)
			return
		}
		fmt.Fprint(w, `{"number":42,"url":"https://github.com/o/r/pull/42"}`)
	case r.URL.Path == "/api/tasks/"+idAsk+"/messages", r.URL.Path == "/api/tasks/"+idAsk+"/stop":
		fmt.Fprint(w, `{"ok":true}`)
	case r.URL.Path == "/api/tasks" && r.Method == http.MethodPost:
		fmt.Fprint(w, `{"id":"33333333-cccc-4ccc-8ccc-000000000003","title":"t","workflow":"implement","status":"running"}`)
	default:
		http.NotFound(w, r)
	}
}

func setup(t *testing.T) (*tools, *fakeHub) {
	f := &fakeHub{t: t, draftID: "draft-1"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &tools{hub: newHubClient(srv.URL, "remote"), repos: map[string]string{"agent-hub": "/repos/agent-hub"}}, f
}

func out(t *testing.T, r *mcp.CallToolResult, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return r.Content[0].(*mcp.TextContent).Text
}

func TestOverviewPutsDecisionsFirstAndHidesDone(t *testing.T) {
	tl, _ := setup(t)
	r, _, err := tl.overview(context.Background(), nil, overviewIn{})
	s := out(t, r, err)
	if !strings.Contains(s, "[等你決定] 11111111｜agent-hub｜plan") || strings.Contains(s, "22222222") {
		t.Errorf("overview:\n%s", s)
	}
	r, _, err = tl.overview(context.Background(), nil, overviewIn{All: true})
	if s := out(t, r, err); !strings.Contains(s, "22222222") {
		t.Errorf("all:\n%s", s)
	}
}

func TestTaskShowsAskAndDraft(t *testing.T) {
	tl, _ := setup(t)
	r, _, err := tl.taskDetail(context.Background(), nil, taskIn{Task: "11111111"})
	s := out(t, r, err)
	for _, want := range []string{"等你回答的問題：Start porting now?", "選項 A：port", "建議：A", "PR 草稿：feat: port", "I checked main.", "只當資料看"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
}

func TestResolveRejectsBadRefs(t *testing.T) {
	tl, _ := setup(t)
	for _, ref := range []string{"../deploy", "1111", "zzzzzzzz", "99999999"} {
		if _, err := tl.resolve(context.Background(), ref); err == nil {
			t.Errorf("resolve(%q) should fail", ref)
		}
	}
}

func TestReplyAndStartUseNarrowCalls(t *testing.T) {
	tl, f := setup(t)
	rr, _, rerr := tl.reply(context.Background(), nil, replyIn{Task: "11111111", Text: "A，開始做"})
	out(t, rr, rerr)
	r, _, err := tl.start(context.Background(), nil, startIn{Workflow: "implement", Repo: "agent-hub", Title: "fix typo", Message: "fix the README typo"})
	if s := out(t, r, err); !strings.Contains(s, "已開任務 33333333") {
		t.Errorf("start: %s", s)
	}
	if f.posts[0] != "/api/tasks/"+idAsk+"/messages" || f.bodies[0]["text"] != "A，開始做" {
		t.Errorf("reply posted %v %v", f.posts, f.bodies)
	}
	if f.bodies[1]["repoPath"] != "/repos/agent-hub" || f.bodies[1]["agent"] != "codex" {
		t.Errorf("start body %v", f.bodies[1])
	}
	r, _, err = tl.start(context.Background(), nil, startIn{Workflow: "implement", Repo: "/etc", Title: "x", Message: "y"})
	if s := out(t, r, err); !strings.Contains(s, "沒有這個 repo 別名") || len(f.posts) != 2 {
		t.Errorf("unknown repo should not post: %s %v", s, f.posts)
	}
	r, _, err = tl.start(context.Background(), nil, startIn{Workflow: "plan", Title: "x", Message: "y"})
	if s := out(t, r, err); !strings.Contains(s, "只能是") {
		t.Errorf("plan: %s", s)
	}
}

func TestPRNeedsTheDraftTheOwnerSaw(t *testing.T) {
	tl, f := setup(t)
	r, _, err := tl.pr(context.Background(), nil, prIn{Task: "11111111"})
	if s := out(t, r, err); !strings.Contains(s, "草稿代號 draft-1") {
		t.Errorf("draft: %s", s)
	}
	r, _, err = tl.pr(context.Background(), nil, prIn{Task: "11111111", Confirm: true, DraftID: "stale"})
	if s := out(t, r, err); !strings.Contains(s, "對不上") {
		t.Errorf("stale draft: %s", s)
	}
	r, _, err = tl.pr(context.Background(), nil, prIn{Task: "11111111", Confirm: true, DraftID: "draft-1"})
	if s := out(t, r, err); !strings.Contains(s, "#42") {
		t.Errorf("open: %s", s)
	}
	last := f.bodies[len(f.bodies)-1]
	if last["draftId"] != "draft-1" || last["branch"] != "agent-hub/x" || last["commitMessage"] != "feat: port" {
		t.Errorf("confirmation %v", last)
	}
	f.forbidPR = true
	r, _, err = tl.pr(context.Background(), nil, prIn{Task: "11111111", Confirm: true, DraftID: "draft-1"})
	if s := out(t, r, err); !strings.Contains(s, "不開放從 Discord 做") {
		t.Errorf("403: %s", s)
	}
}

func TestParseRepos(t *testing.T) {
	got := parseRepos("agent-hub=/home/u/agent-hub, bad=relative, =/x,cara=/home/u/cara")
	if len(got) != 2 || got["cara"] != "/home/u/cara" {
		t.Errorf("%v", got)
	}
}

// 工具註冊時才會檢查 jsonschema 標籤，單測 handler 不會經過這一步。
func TestServerRegistersAllTools(t *testing.T) {
	tl, _ := setup(t)
	newServer(tl)
}

func TestWatcherNotifiesOnceWhenTaskStops(t *testing.T) {
	var polls int
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++
		status := "running"
		if polls >= 3 {
			status = "idle"
		}
		fmt.Fprintf(w, `{"id":%q,"title":"t","workflow":"chat","status":%q}`, idAsk, status)
	}))
	defer hub.Close()
	got := make(chan map[string]any, 2)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		got <- b
		w.WriteHeader(204)
	}))
	defer hook.Close()
	w := newWatcher(newHubClient(hub.URL, "remote"), hook.URL, "999")
	w.poll = time.Millisecond
	w.watch(idAsk)
	w.watch(idAsk) // 重複的不會再盯一次
	select {
	case b := <-got:
		if c, _ := b["content"].(string); !strings.HasPrefix(c, "<@999> 任務 11111111 跑完了") {
			t.Errorf("content = %q", c)
		}
		am, _ := b["allowed_mentions"].(map[string]any)
		if users, _ := am["users"].([]any); len(users) != 1 || users[0] != "999" {
			t.Errorf("allowed_mentions = %v", am)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notice")
	}
	select {
	case b := <-got:
		t.Errorf("second notice: %v", b)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFollowUpWithoutWebhook(t *testing.T) {
	tl, _ := setup(t)
	if s := tl.followUp(idAsk); !strings.Contains(s, "不會自動通知") {
		t.Errorf("%s", s)
	}
}
