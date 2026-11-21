// Command fakellama stands in for llama-server in tests: /health, /v1/models and an
// OpenAI-style /v1/chat/completions that always answers with $FAKE_REPLY.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
)

func main() {
	port := flag.Int("port", 0, "")
	flag.String("m", "", "")
	flag.String("host", "", "")
	flag.String("c", "", "")
	flag.String("parallel", "", "")
	flag.Bool("no-webui", false, "")
	flag.String("sleep-idle-seconds", "", "")
	flag.String("ngl", "", "")
	keyFile := flag.String("api-key-file", "", "")
	flag.Parse()
	key, _ := os.ReadFile(*keyFile)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok"}`)) })
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"object":"list","data":[{"id":"fake"}]}`))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if len(key) == 0 || r.Header.Get("Authorization") != "Bearer "+string(key) { // like the real server, refuse callers without the key
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		b, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": os.Getenv("FAKE_REPLY")}}}})
		w.Write(b)
	})
	_ = http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", *port), mux)
}
