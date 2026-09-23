package service

import (
	"context"
	"fmt"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/schedule"
)

type Occurrence struct {
	Time   time.Time `json:"time"`
	Host   string    `json:"host"`
	Source string    `json:"source"`
	JobID  string    `json:"job_id"`
	Name   string    `json:"name"`
	Queued bool      `json:"queued"`
}
type Cell struct {
	Day       int  `json:"day"`
	Hour      int  `json:"hour"`
	Count     int  `json:"count"`
	Truncated bool `json:"truncated"`
}
type Overview struct {
	Start     time.Time    `json:"start"`
	End       time.Time    `json:"end"`
	Timezone  string       `json:"timezone"`
	Cells     [7][24]Cell  `json:"cells"`
	Agenda    []Occurrence `json:"agenda"`
	Warnings  []string     `json:"warnings"`
	Truncated bool         `json:"truncated"`
}

func WeekStart(t time.Time) *time.Time {
	day := int(t.Weekday())
	if day == 0 {
		day = 7
	}
	start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).AddDate(0, 0, 1-day)
	return &start
}
func BuildOverview(ctx context.Context, snaps []Snapshot, date time.Time, loc *time.Location, limit int) (Overview, error) {
	start := *WeekStart(date.In(loc))
	o := Overview{Start: start, End: start.AddDate(0, 0, 7), Timezone: loc.String(), Agenda: []Occurrence{}, Warnings: []string{}}
	for d := range 7 {
		for h := range 24 {
			o.Cells[d][h] = Cell{Day: d, Hour: h}
		}
	}
	if limit < 1 || limit > 100000 {
		return o, fmt.Errorf("agenda limit must be 1..100000")
	}
	for _, snap := range snaps {
		if snap.Error != "" {
			o.Warnings = append(o.Warnings, snap.Host+"/"+snap.Source+": "+snap.Error)
			continue
		}
		for _, j := range snap.Entries {
			if !j.Enabled {
				continue
			}
			if j.Diagnostic != "" {
				o.Warnings = append(o.Warnings, j.Key()+": "+j.Diagnostic)
				continue
			}
			jl, err := time.LoadLocation(j.Timezone)
			if err != nil {
				o.Warnings = append(o.Warnings, j.Key()+": unknown timezone")
				continue
			}
			sc, err := schedule.Parse(j.Schedule, j.Dialect, jl, "en")
			if err != nil || sc.Event {
				continue
			}
			first := sc.Next(o.Start.Add(-time.Nanosecond))
			if first.IsZero() || !first.Before(o.End) {
				continue
			}
			// Bound work independently per hourly bucket. Repeated DST hours are
			// traversed as distinct instants and coalesced into the visible cell.
			for from := o.Start; from.Before(o.End); from = from.Add(time.Hour) {
				to := from.Add(time.Hour)
				if to.After(o.End) {
					to = o.End
				}
				d := 0
				for d < 6 && !from.Before(o.Start.AddDate(0, 0, d+1)) {
					d++
				}
				h := from.In(loc).Hour()
				cell := &o.Cells[d][h]
				if cell.Count >= 10000 {
					cell.Truncated = true
					o.Truncated = true
					continue
				}
				cursor := from.Add(-time.Nanosecond)
				for n := 0; n < 10000; n++ {
					if err := ctx.Err(); err != nil {
						return o, err
					}
					next := sc.Next(cursor)
					if next.IsZero() || !next.Before(to) {
						break
					}
					if !next.After(cursor) {
						return o, fmt.Errorf("schedule did not advance")
					}
					cursor = next
					cell.Count++
					if cell.Count >= 10000 {
						cell.Truncated = true
						o.Truncated = true
						break
					}
					if n == 9999 {
						cell.Truncated = true
						o.Truncated = true
					}
				}
			}
		}
	}
	// Merge fresh iterators so a per-job cap cannot bias the agenda.
	var more bool
	var err error
	o.Agenda, more, err = Agenda(ctx, snaps, o.Start, o.End, 0, limit)
	if err != nil {
		return o, err
	}
	o.Truncated = o.Truncated || more
	return o, nil
}

// Agenda computes an exact, bounded merge for the selected time interval.
func Agenda(ctx context.Context, snaps []Snapshot, from, to time.Time, offset, limit int) ([]Occurrence, bool, error) {
	type iterator struct {
		entry Entry
		sc    *schedule.Schedule
		next  time.Time
	}
	its := []iterator{}
	for _, s := range snaps {
		if s.Error != "" {
			continue
		}
		for _, e := range s.Entries {
			if !e.Enabled || e.Diagnostic != "" {
				continue
			}
			loc, err := time.LoadLocation(e.Timezone)
			if err != nil {
				continue
			}
			sc, err := schedule.Parse(e.Schedule, e.Dialect, loc, "en")
			if err != nil || sc.Event {
				continue
			}
			next := sc.Next(from.Add(-time.Nanosecond))
			if !next.IsZero() && next.Before(to) {
				its = append(its, iterator{e, sc, next})
			}
		}
	}
	out := []Occurrence{}
	for n := 0; n < offset+limit+1; n++ {
		if err := ctx.Err(); err != nil {
			return out, false, err
		}
		best := -1
		for i := range its {
			if !its[i].next.IsZero() && its[i].next.Before(to) && (best < 0 || its[i].next.Before(its[best].next)) {
				best = i
			}
		}
		if best < 0 {
			return out, false, nil
		}
		it := &its[best]
		if n >= offset {
			if len(out) == limit {
				return out, true, nil
			}
			out = append(out, Occurrence{Time: it.next.In(from.Location()), Host: it.entry.Host, Source: it.entry.Source, JobID: it.entry.ID, Name: it.entry.Name, Queued: it.entry.Runner == "pueue"})
		}
		next := it.sc.Next(it.next)
		if !next.IsZero() && !next.After(it.next) {
			return out, false, fmt.Errorf("schedule did not advance")
		}
		it.next = next
	}
	return out, false, nil
}
