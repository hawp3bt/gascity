module github.com/myfork/gascity

go 1.22

require (
	github.com/go-chi/chi/v5 v5.1.0
	github.com/go-chi/cors v1.2.1
	github.com/joho/godotenv v1.5.1
	go.uber.org/zap v1.27.0
)

require (
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
)

// Note: forked from gastownhall/gascity for personal learning.
// go.uber.org/atomic is listed here for clarity even though zap v1.27.0
// no longer strictly requires it (kept for reference during study).
//
// TODO: experiment with replacing go-chi/cors with a custom middleware
// to better understand how CORS headers work under the hood.
