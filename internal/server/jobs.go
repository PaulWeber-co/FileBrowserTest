package server

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/PaulWeber-co/FileBrowserTest/internal/config"
)

// JobState beschreibt den Zustand eines Hintergrundauftrags.
type JobState string

const (
	JobRunning   JobState = "running"
	JobDone      JobState = "done"
	JobError     JobState = "error"
	JobCancelled JobState = "cancelled"
)

// Job ist ein laufender Kopier- oder Verschiebevorgang.
type Job struct {
	ID        string    `json:"id"`
	Op        string    `json:"op"`
	Owner     string    `json:"-"`
	Label     string    `json:"label"`
	State     JobState  `json:"state"`
	Current   string    `json:"current"`
	Files     int       `json:"files"`
	FilesDone int       `json:"filesDone"`
	Total     int64     `json:"total"`
	Done      int64     `json:"done"`
	Error     string    `json:"error,omitempty"`
	Started   time.Time `json:"started"`
	Finished  time.Time `json:"finished,omitempty"`
	Rate      float64   `json:"rate"` // Bytes pro Sekunde

	cancel context.CancelFunc
	mu     sync.Mutex
}

// JobView ist die Kopie eines Auftrags für die API - ohne Mutex, damit sie
// gefahrlos herumgereicht werden kann.
type JobView struct {
	ID        string    `json:"id"`
	Op        string    `json:"op"`
	Label     string    `json:"label"`
	State     JobState  `json:"state"`
	Current   string    `json:"current"`
	Files     int       `json:"files"`
	FilesDone int       `json:"filesDone"`
	Total     int64     `json:"total"`
	Done      int64     `json:"done"`
	Error     string    `json:"error,omitempty"`
	Started   time.Time `json:"started"`
	Finished  time.Time `json:"finished,omitempty"`
	Rate      float64   `json:"rate"`
}

func (j *Job) snapshot() JobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	v := JobView{
		ID: j.ID, Op: j.Op, Label: j.Label, State: j.State, Current: j.Current,
		Files: j.Files, FilesDone: j.FilesDone, Total: j.Total, Done: j.Done,
		Error: j.Error, Started: j.Started, Finished: j.Finished,
	}
	elapsed := time.Since(j.Started).Seconds()
	if !j.Finished.IsZero() {
		elapsed = j.Finished.Sub(j.Started).Seconds()
	}
	if elapsed > 0.2 {
		v.Rate = float64(j.Done) / elapsed
	}
	return v
}

func (j *Job) addBytes(n int64) {
	j.mu.Lock()
	j.Done += n
	j.mu.Unlock()
}

func (j *Job) setCurrent(name string) {
	j.mu.Lock()
	j.Current = name
	j.mu.Unlock()
}

func (j *Job) fileDone() {
	j.mu.Lock()
	j.FilesDone++
	j.mu.Unlock()
}

func (j *Job) setPlan(files int, total int64) {
	j.mu.Lock()
	j.Files, j.Total = files, total
	j.mu.Unlock()
}

func (j *Job) finish(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Finished = time.Now()
	switch {
	case err == nil:
		j.State = JobDone
	case err == context.Canceled:
		j.State = JobCancelled
	default:
		j.State = JobError
		j.Error = friendly(err)
	}
}

// JobManager verwaltet die laufenden Aufträge.
type JobManager struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

// NewJobManager erzeugt eine Auftragsverwaltung.
func NewJobManager() *JobManager { return &JobManager{jobs: map[string]*Job{}} }

// Start legt einen Auftrag an und führt fn im Hintergrund aus.
func (m *JobManager) Start(owner, op, label string, fn func(ctx context.Context, j *Job) error) *Job {
	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{
		ID:      config.NewID(),
		Op:      op,
		Owner:   owner,
		Label:   label,
		State:   JobRunning,
		Started: time.Now(),
		cancel:  cancel,
	}
	m.mu.Lock()
	m.jobs[j.ID] = j
	m.mu.Unlock()

	go func() {
		defer cancel()
		err := fn(ctx, j)
		j.finish(err)
		// Abgeschlossene Aufträge noch kurz vorhalten, damit die Oberfläche
		// das Ergebnis anzeigen kann.
		time.AfterFunc(5*time.Minute, func() {
			m.mu.Lock()
			delete(m.jobs, j.ID)
			m.mu.Unlock()
		})
	}()
	return j
}

// List liefert die Aufträge eines Benutzers, neueste zuerst.
func (m *JobManager) List(owner string, all bool) []JobView {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]JobView, 0, len(m.jobs))
	for _, j := range m.jobs {
		if all || j.Owner == owner {
			out = append(out, j.snapshot())
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Started.After(out[k].Started) })
	return out
}

// Get liefert einen einzelnen Auftrag.
func (m *JobManager) Get(id string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	return j, ok
}

// Cancel bricht einen Auftrag ab.
func (m *JobManager) Cancel(id, owner string, admin bool) bool {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok || (!admin && j.Owner != owner) {
		return false
	}
	j.cancel()
	return true
}

// CancelAll bricht alle laufenden Aufträge ab (beim Beenden).
func (m *JobManager) CancelAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		j.cancel()
	}
}
