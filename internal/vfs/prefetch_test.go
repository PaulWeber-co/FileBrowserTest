package vfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"
)

// fakeReaderAt liefert Daten aus dem Speicher und zaehlt die Zugriffe.
type fakeReaderAt struct {
	data   []byte
	reads  atomic.Int64
	delay  time.Duration
	closed atomic.Bool
	failAt int64 // ab diesem Offset Fehler liefern; -1 = nie
}

func (f *fakeReaderAt) ReadAt(p []byte, off int64) (int, error) {
	f.reads.Add(1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.failAt >= 0 && off >= f.failAt {
		return 0, errors.New("simulierter Lesefehler")
	}
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *fakeReaderAt) Close() error { f.closed.Store(true); return nil }

func TestPrefetchReaderDeliversExactBytes(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	sizes := []int{0, 1, 1023, 64 << 10, (1 << 20) + 7, 3 * (1 << 20)}
	for _, size := range sizes {
		data := make([]byte, size)
		rnd.Read(data)
		for _, workers := range []int{1, 2, 4, 8} {
			for _, off := range []int64{0, 1, int64(size) / 3} {
				if off > int64(size) {
					continue
				}
				fr := &fakeReaderAt{data: data, failAt: -1}
				r := NewPrefetchReader(context.Background(), fr, int64(size), off,
					PrefetchOpts{Workers: workers, ChunkSize: 128 << 10})
				got, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil {
					t.Fatalf("size=%d workers=%d off=%d: %v", size, workers, off, err)
				}
				want := data[off:]
				if !bytes.Equal(got, want) {
					t.Fatalf("size=%d workers=%d off=%d: %d Bytes gelesen, erwartet %d",
						size, workers, off, len(got), len(want))
				}
			}
		}
	}
}

func TestPrefetchReaderIsFasterWithLatency(t *testing.T) {
	// Kernaussage des Verfahrens: bei Latenz gewinnt Parallelitaet.
	data := make([]byte, 8<<20)
	rand.New(rand.NewSource(1)).Read(data)

	measure := func(workers int) time.Duration {
		fr := &fakeReaderAt{data: data, delay: 5 * time.Millisecond, failAt: -1}
		start := time.Now()
		r := NewPrefetchReader(context.Background(), fr, int64(len(data)), 0,
			PrefetchOpts{Workers: workers, ChunkSize: 512 << 10})
		_, _ = io.Copy(io.Discard, r)
		_ = r.Close()
		return time.Since(start)
	}
	serial := measure(1)
	parallel := measure(4)
	if parallel >= serial {
		t.Errorf("parallel (%v) sollte schneller sein als seriell (%v)", parallel, serial)
	}
}

func TestPrefetchReaderPropagatesError(t *testing.T) {
	data := make([]byte, 4<<20)
	fr := &fakeReaderAt{data: data, failAt: 1 << 20}
	r := NewPrefetchReader(context.Background(), fr, int64(len(data)), 0,
		PrefetchOpts{Workers: 2, ChunkSize: 256 << 10})
	_, err := io.ReadAll(r)
	_ = r.Close()
	if err == nil {
		t.Fatal("Fehler wurde verschluckt")
	}
}

func TestPrefetchReaderCloseReleasesSource(t *testing.T) {
	data := make([]byte, 4<<20)
	fr := &fakeReaderAt{data: data, delay: time.Millisecond, failAt: -1}
	r := NewPrefetchReader(context.Background(), fr, int64(len(data)), 0,
		PrefetchOpts{Workers: 4, ChunkSize: 64 << 10})
	buf := make([]byte, 1024)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	// Das Aufraeumen laeuft nebenlaeufig; kurz warten.
	deadline := time.Now().Add(2 * time.Second)
	for !fr.closed.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !fr.closed.Load() {
		t.Error("Quelle wurde nach Close nicht freigegeben")
	}
}

func TestPrefetchOptsNormalisiert(t *testing.T) {
	o := PrefetchOpts{Workers: 0, ChunkSize: 1}.norm()
	if o.Workers != 1 || o.ChunkSize != 64<<10 {
		t.Errorf("Untergrenzen nicht angewandt: %+v", o)
	}
	o = PrefetchOpts{Workers: 999, ChunkSize: 1 << 30}.norm()
	if o.Workers != 16 || o.ChunkSize != 8<<20 {
		t.Errorf("Obergrenzen nicht angewandt: %+v", o)
	}
}
