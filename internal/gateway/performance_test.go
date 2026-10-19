package gateway

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

func BenchmarkRuntimeRecord(b *testing.B) {
	for _, capacity := range []int{1000, 10000} {
		b.Run(fmt.Sprint(capacity), func(b *testing.B) {
			s := newRuntime()
			log := RequestLog{ID: "request", Time: time.Now().UTC(), Status: 200, DurationMS: 10}
			for range capacity {
				s.record(log, capacity)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				s.record(log, capacity)
			}
		})
	}
}

func BenchmarkRuntimeMetrics(b *testing.B) {
	s := newRuntime()
	for i := range 10000 {
		s.record(RequestLog{Time: time.Now().UTC().Add(-time.Duration(i%30) * time.Minute), Status: 200}, 10000)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.metrics()
	}
}

func BenchmarkStreamObserve(b *testing.B) {
	chunk := append([]byte(`data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"`), bytes.Repeat([]byte("text "), 400)...)
	chunk = append(chunk, []byte("\"}}]}\n\n")...)
	b.SetBytes(int64(len(chunk) * 100))
	b.ReportAllocs()
	for b.Loop() {
		o := observer{}
		for range 100 {
			if err := o.write(chunk); err != nil {
				b.Fatal(err)
			}
		}
	}
}
