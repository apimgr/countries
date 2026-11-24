package server

import (
	"expvar"
	"runtime"
	"time"
)

var (
	requestCount    = expvar.NewInt("requests_total")
	requestDuration = expvar.NewFloat("requests_duration_seconds")
	errorCount      = expvar.NewInt("errors_total")
	serverStartTime = time.Now()
)

func init() {
	expvar.Publish("uptime_seconds", expvar.Func(func() interface{} {
		return time.Since(serverStartTime).Seconds()
	}))
	expvar.Publish("goroutines", expvar.Func(func() interface{} {
		return runtime.NumGoroutine()
	}))
	expvar.Publish("memory", expvar.Func(func() interface{} {
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		return map[string]uint64{
			"alloc":       stats.Alloc,
			"total_alloc": stats.TotalAlloc,
			"sys":         stats.Sys,
			"heap_alloc":  stats.HeapAlloc,
			"heap_sys":    stats.HeapSys,
		}
	}))
}

func recordRequest(duration time.Duration, status int) {
	requestCount.Add(1)
	requestDuration.Add(duration.Seconds())
	if status >= 400 {
		errorCount.Add(1)
	}
}
