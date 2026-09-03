package vfs

import (
	"context"
	"io"
	"sync"
)

// PrefetchOpts steuert das parallele Vorauslesen.
type PrefetchOpts struct {
	Workers   int   // Anzahl gleichzeitiger Leseanfragen
	ChunkSize int64 // Größe einer Anfrage in Bytes
}

// DefaultPrefetch ist ein guter Kompromiss für USB-2.0-Speicher am Router.
var DefaultPrefetch = PrefetchOpts{Workers: 4, ChunkSize: 1 << 20}

func (o PrefetchOpts) norm() PrefetchOpts {
	if o.Workers < 1 {
		o.Workers = 1
	}
	if o.Workers > 16 {
		o.Workers = 16
	}
	if o.ChunkSize < 64<<10 {
		o.ChunkSize = 64 << 10
	}
	if o.ChunkSize > 8<<20 {
		o.ChunkSize = 8 << 20
	}
	return o
}

type chunkResult struct {
	buf []byte
	err error
}

// prefetchReader liest eine Datei mit mehreren parallelen ReadAt-Anfragen und
// liefert die Bytes trotzdem streng der Reihe nach aus.
//
// Warum das der entscheidende Trick ist: ein einzelner sequentieller SMB-Read
// ist latenzgebunden. Bei 40 ms RTT über VPN und 64-KiB-Antworten liegt die
// Obergrenze rechnerisch bei rund 1,6 MB/s - egal wie schnell die Leitung ist.
// Vier parallele 1-MiB-Anfragen halten die Leitung dagegen dauerhaft gefüllt.
type prefetchReader struct {
	futures <-chan chan chunkResult
	cancel  context.CancelFunc
	cur     []byte
	err     error
	once    sync.Once
	closed  chan struct{}
	ra      io.Closer
}

// NewPrefetchReader liefert einen Reader ab Offset off bis Dateiende.
func NewPrefetchReader(ctx context.Context, ra ReaderAtCloser, size, off int64, opts PrefetchOpts) io.ReadCloser {
	opts = opts.norm()
	ctx, cancel := context.WithCancel(ctx)
	futures := make(chan chan chunkResult, opts.Workers)
	closed := make(chan struct{})

	go func() {
		defer close(futures)
		for pos := off; pos < size; pos += opts.ChunkSize {
			n := opts.ChunkSize
			if rest := size - pos; rest < n {
				n = rest
			}
			f := make(chan chunkResult, 1)
			select {
			case futures <- f:
			case <-ctx.Done():
				return
			}
			go func(pos int64, n int64) {
				buf := make([]byte, n)
				read, err := ra.ReadAt(buf, pos)
				if err == io.EOF && int64(read) == n {
					err = nil
				}
				f <- chunkResult{buf: buf[:read], err: err}
			}(pos, n)
		}
	}()

	return &prefetchReader{futures: futures, cancel: cancel, closed: closed, ra: ra}
}

func (r *prefetchReader) Read(p []byte) (int, error) {
	for len(r.cur) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		f, ok := <-r.futures
		if !ok {
			r.err = io.EOF
			return 0, io.EOF
		}
		select {
		case res := <-f:
			if res.err != nil {
				r.err = res.err
				if len(res.buf) == 0 {
					return 0, r.err
				}
			}
			r.cur = res.buf
		case <-r.closed:
			return 0, io.ErrClosedPipe
		}
	}
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}

func (r *prefetchReader) Close() error {
	var err error
	r.once.Do(func() {
		r.cancel()
		close(r.closed)
		// Ausstehende Chunks abräumen, damit keine Goroutine hängen bleibt.
		go func() {
			for f := range r.futures {
				<-f
			}
			_ = r.ra.Close()
		}()
	})
	return err
}
