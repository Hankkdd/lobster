// hub-mcp 讓 Discord 上的 Hub bot操作 Agent Hub：看任務、回覆、停止、開任務、開 PR。
//
// 拿的是 Hub 的 remote token（權限本身就被 Hub 限制住），而且只有綁在 #agent-hub、
// 只准擁有者使用的那個 OpenClaw agent 拿得到這些工具。比價 bot會讀 FB 貼文和網頁，
// 那些內容任何人都能寫，所以它的設定裡把 hub__* 擋掉。
package main

import (
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	hubToken := strings.TrimSpace(readFile(mustEnv("HUB_TOKEN_FILE")))
	if hubToken == "" {
		log.Fatal("HUB_TOKEN_FILE 是空的")
	}
	hub := newHubClient(mustEnv("HUB_URL"), hubToken)
	// 選填：有設才在任務跑完時 tag bot回報
	t := &tools{hub: hub, repos: parseRepos(os.Getenv("HUB_REPOS")),
		watch: newWatcher(hub, os.Getenv("HUB_NOTIFY_WEBHOOK"), os.Getenv("HUB_NOTIFY_BOT_ID"))}

	server := newServer(t)

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	mux := http.NewServeMux()
	mux.Handle("/mcp", requireToken(mustEnv("HUB_MCP_TOKEN"), handler))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	addr := envOr("HUB_MCP_ADDR", ":8080")
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("hub mcp listening on %s, repos: %v, finish notices: %t", addr, t.repoNames(), t.watch.enabled())
	log.Fatal(srv.ListenAndServe())
}

func newServer(t *tools) *mcp.Server {
	no, yes := false, true
	server := mcp.NewServer(&mcp.Implementation{Name: "hub", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "overview",
		Description: "列出 Agent Hub 裡等擁有者決定、出問題、等驗收或正在跑的任務。",
		Annotations: &mcp.ToolAnnotations{Title: "Hub 任務總覽", ReadOnlyHint: true, OpenWorldHint: &no}}, t.overview)
	mcp.AddTool(server, &mcp.Tool{Name: "task",
		Description: "看一個任務的狀態、等擁有者回答的問題與選項、PR 草稿、agent 最近的回覆。",
		Annotations: &mcp.ToolAnnotations{Title: "Hub 任務內容", ReadOnlyHint: true, OpenWorldHint: &no}}, t.taskDetail)
	mcp.AddTool(server, &mcp.Tool{Name: "reply",
		Description: "把擁有者的話傳給任務（回答等你決定的問題、追加指示）。只傳擁有者明確要你傳的內容。",
		Annotations: &mcp.ToolAnnotations{Title: "回覆 Hub 任務", DestructiveHint: &no, OpenWorldHint: &no}}, t.reply)
	mcp.AddTool(server, &mcp.Tool{Name: "stop",
		Description: "停止正在跑的任務。",
		Annotations: &mcp.ToolAnnotations{Title: "停止 Hub 任務", DestructiveHint: &yes, OpenWorldHint: &no}}, t.stop)
	mcp.AddTool(server, &mcp.Tool{Name: "start",
		Description: "開新任務（一律 Codex、不帶 Devices）。chat 問問題，implement/review 改程式，repo 用別名。",
		Annotations: &mcp.ToolAnnotations{Title: "開 Hub 任務", DestructiveHint: &no, OpenWorldHint: &no}}, t.start)
	mcp.AddTool(server, &mcp.Tool{Name: "pr",
		Description: "準備 PR 草稿給擁有者看（confirm=false），他同意後用草稿代號開 PR（confirm=true）。",
		Annotations: &mcp.ToolAnnotations{Title: "開 PR", DestructiveHint: &no, OpenWorldHint: &yes}}, t.pr)

	return server
}

// parseRepos 讀「別名=路徑,別名=路徑」。路徑還要在 Hub 的 repos.json 裡，Hub 那邊會再擋一次。
func parseRepos(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && k != "" && strings.HasPrefix(v, "/") {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func requireToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		log.Fatalf("讀不到 %s: %v", p, err)
	}
	return string(b)
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("請設定 %s", k)
	}
	return v
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
