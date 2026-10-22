package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"telesrv/internal/botapi"
	"testing"
)

func joinHTTPUpdates(t *testing.T, f botAPIBridgeFixture) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithCancel(f.ctx)
	server, err := botapi.Start(ctx, "127.0.0.1:0", f.bots, f.users, nil, nil, f.router, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer cancel()
	defer server.Close()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/bot%d:secret/getUpdates", f.bot.ID), strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	server.Handler.ServeHTTP(rec, req)
	var response struct {
		OK     bool             `json:"ok"`
		Result []map[string]any `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || !response.OK {
		t.Fatalf("getUpdates: %s (%v)", rec.Body.String(), err)
	}
	return response.Result
}
