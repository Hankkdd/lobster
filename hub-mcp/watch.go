package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// watcher 盯著從 Discord 開的或回覆過的任務，任務一停下來（做完、失敗、等人決定），
// 就在 #agent-hub 貼一則 tag bot的通知，讓他讀結果回報給擁有者。
//
// Hub 自己對 chat、plan 跑完不發通知，implement、review 跑完是靜音的；
// 而 OpenClaw 的 bot只在被 tag 時才動，沒有這個就沒人知道任務跑完了。
//
// 通知內容只有任務 ID 和狀態，是這裡固定寫的，不帶任何任務裡的文字。
// 名單只在記憶體裡：服務重啟就不盯了，最多是少一則回報，任務本身不受影響。
type watcher struct {
	hub     *hubClient
	webhook string // #agent-hub 的 webhook；空的話不通知
	botID   string // 要 tag 的 bot
	poll    time.Duration
	limit   time.Duration
	client  *http.Client

	mu       sync.Mutex
	watching map[string]bool
}

func newWatcher(hub *hubClient, webhook, botID string) *watcher {
	return &watcher{hub: hub, webhook: webhook, botID: botID, poll: 15 * time.Second, limit: 3 * time.Hour,
		client: &http.Client{Timeout: 15 * time.Second}, watching: map[string]bool{}}
}

func (w *watcher) enabled() bool { return w != nil && w.webhook != "" && w.botID != "" }

// watch 開始盯一個任務。同一個任務已經在盯就不重複。
func (w *watcher) watch(id string) {
	if !w.enabled() {
		return
	}
	w.mu.Lock()
	if w.watching[id] {
		w.mu.Unlock()
		return
	}
	w.watching[id] = true
	w.mu.Unlock()
	go w.run(id)
}

// stopped 是任務停下來、需要回報的狀態。
func stopped(status string) bool {
	switch status {
	case "idle", "done", "failed", "waiting_user", "paused", "interrupted":
		return true
	}
	return false
}

func (w *watcher) run(id string) {
	defer func() {
		w.mu.Lock()
		delete(w.watching, id)
		w.mu.Unlock()
	}()
	deadline := time.Now().Add(w.limit)
	seenRunning := false
	for time.Now().Before(deadline) {
		time.Sleep(w.poll)
		tk, err := w.hub.task(context.Background(), id)
		if err != nil {
			continue // Hub 暫時連不上就下一輪再看
		}
		if tk.Status == "running" {
			seenRunning = true
			continue
		}
		// 剛開的任務可能還沒排進去；沒看過 running 就多等一輪，避免一開始就誤報
		if !seenRunning && time.Until(deadline) > w.limit-2*w.poll {
			continue
		}
		if stopped(tk.Status) {
			w.notify(id, tk.Status)
			return
		}
	}
}

var statusLabel = map[string]string{
	"idle": "跑完了", "done": "完成了", "failed": "失敗了", "waiting_user": "在等你決定",
	"paused": "暫停了（額度用完）", "interrupted": "被中斷了",
}

func (w *watcher) notify(id, status string) {
	msg := map[string]any{
		"content":  fmt.Sprintf("<@%s> 任務 %s %s。請讀結果回報給擁有者。", w.botID, id[:8], statusLabel[status]),
		"username": "Hub 回報",
		// 只准 tag bot，不讓任何人被意外 ping 到
		"allowed_mentions": map[string]any{"parse": []string{}, "users": []string{w.botID}},
	}
	b, _ := json.Marshal(msg)
	resp, err := w.client.Post(w.webhook, "application/json", bytes.NewReader(b))
	if err != nil {
		log.Printf("任務 %s 完成通知送不出去: %v", id[:8], err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		log.Printf("任務 %s 完成通知 HTTP %d", id[:8], resp.StatusCode)
	}
}
