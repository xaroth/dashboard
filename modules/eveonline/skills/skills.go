// Package skills polls the skill queues and trained skills of the logged-in
// EVE Online characters from ESI.
package skills

import (
	"cmp"
	"context"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/eve-online-tools/lib-esi-go/common/character"
	"github.com/eve-online-tools/lib-esi-go/esi/getcharacterscharacteridskillqueue"
	"github.com/eve-online-tools/lib-esi-go/esi/getcharacterscharacteridskills"
	"github.com/eve-online-tools/lib-esi-go/esi/postuniversenames"
	"github.com/eve-online-tools/lib-esi-go/middleware/authentication"
	"github.com/eve-online-tools/lib-esi-go/request"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/modules/eveonline"
)

//go:embed sql/migrations/*.sql
var migrations embed.FS

var scopes = eveonline.Scopes{"esi-skills.read_skillqueue.v1", "esi-skills.read_skills.v1"}

type config struct{}

type skills struct{}

// Module is the eveonline_skills registration.
var Module = module.Define[skills, config](
	"eveonline_skills",
	func() *config { return &config{} },
	// The queue only changes in game; queries count down from the stored
	// finish dates, so polling more often than ESI's 60 s cache adds nothing.
	module.WithUpdateInterval(15*time.Minute),
	module.WithStaleAfter(2*time.Hour),
	module.WithShared(eveonline.ESI, scopes),
	module.WithMigrations(migrations),
	module.WithQuery("queue", (*skills).queue),
)

// snapshot is what one update stores: every character's queue and skills
// as ESI returned them, finished queue entries included.
type snapshot struct {
	Characters []stored `json:"characters"`
}

type stored struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// FetchedAt is the last successful fetch of this character; Error is
	// why the latest one failed, which keeps the previous data.
	FetchedAt *time.Time `json:"fetchedAt"`
	Error     string     `json:"error"`
	Data      fetched    `json:"data"`
}

type fetched struct {
	Queue         []entry   `json:"queue"`
	Skills        []trained `json:"skills"`
	TotalSP       int64     `json:"totalSp"`
	UnallocatedSP int64     `json:"unallocatedSp"`
}

type entry struct {
	Skill
	// EndSP is the skill's points once the entry finishes.
	EndSP *int64 `json:"endSp"`
}

type trained struct {
	ID    int64 `json:"id"`
	Level int64 `json:"level"`
	SP    int64 `json:"sp"`
}

// Skill is one queue entry. Start and Finish are null while the queue is
// paused.
type Skill struct {
	ID     int64      `json:"id"`
	Name   string     `json:"name"`
	Level  int64      `json:"level"`
	Start  *time.Time `json:"start"`
	Finish *time.Time `json:"finish"`
}

func (*skills) Initialize(context.Context, *config) error { return nil }

func (*skills) Enabled(ctx context.Context) bool {
	chars, err := eveonline.ESI.Get(ctx).Characters(ctx, scopes)
	return err == nil && len(chars) > 0
}

// Update fetches every character's queue and skills. A character that fails
// keeps its previous data with the error; the update fails only when all do.
func (*skills) Update(ctx context.Context) error {
	c := eveonline.ESI.Get(ctx)
	chars, err := c.Characters(ctx, scopes)
	if err != nil {
		return err
	}
	var prev snapshot
	if _, err := module.LoadSnapshot(ctx, &prev); err != nil {
		return err
	}
	now := module.Clock(ctx)()
	out := snapshot{Characters: make([]stored, 0, len(chars))}
	var errs []error
	for _, ch := range chars {
		s := stored{ID: ch.ID, Name: ch.Name}
		d, err := fetch(ctx, c, ch.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ch.Name, err))
			module.Logger(ctx).Warn("fetching skills", "character", ch.Name, "err", err)
			s.Error = err.Error()
			if i := slices.IndexFunc(prev.Characters, func(p stored) bool { return p.ID == ch.ID }); i >= 0 {
				s.FetchedAt, s.Data = prev.Characters[i].FetchedAt, prev.Characters[i].Data
			}
		} else {
			s.FetchedAt, s.Data = &now, d
		}
		out.Characters = append(out.Characters, s)
	}
	if len(chars) > 0 && len(errs) == len(chars) {
		return errors.Join(errs...)
	}
	if err := name(ctx, c, out.Characters); err != nil {
		return err
	}
	return module.SaveSnapshot(ctx, out)
}

func fetch(ctx context.Context, c *eveonline.Client, id int64) (fetched, error) {
	tok, err := c.Token(ctx, id)
	if err != nil {
		return fetched{}, err
	}
	in := character.Identifier(id)
	qresp, err := getcharacterscharacteridskillqueue.Request(ctx, c,
		&getcharacterscharacteridskillqueue.Input{Character: in}, authentication.WithToken(tok))
	if err == nil {
		err = check(qresp)
	}
	if err != nil {
		return fetched{}, fmt.Errorf("requesting skill queue: %w", err)
	}
	sresp, err := getcharacterscharacteridskills.Request(ctx, c,
		&getcharacterscharacteridskills.Input{Character: in}, authentication.WithToken(tok))
	if err == nil {
		err = check(sresp)
	}
	if err != nil {
		return fetched{}, fmt.Errorf("requesting skills: %w", err)
	}

	entries := slices.Clone(qresp.Data)
	slices.SortFunc(entries, func(a, b *getcharacterscharacteridskillqueue.Output) int {
		return cmp.Compare(a.QueuePosition, b.QueuePosition)
	})
	out := fetched{Queue: make([]entry, 0, len(entries)), Skills: []trained{}}
	for _, e := range entries {
		out.Queue = append(out.Queue, entry{
			Skill: Skill{ID: int64(e.Skill), Level: e.FinishedLevel, Start: e.StartDate, Finish: e.FinishDate},
			EndSP: e.LevelEndSp,
		})
	}
	if d := sresp.Data; d != nil {
		out.TotalSP = d.TotalSp
		if d.UnallocatedSp != nil {
			out.UnallocatedSP = *d.UnallocatedSp
		}
		for _, k := range d.Skills {
			out.Skills = append(out.Skills, trained{ID: k.SkillId, Level: k.TrainedSkillLevel, SP: k.SkillpointsInSkill})
		}
	}
	return out, nil
}

func check[T any](resp *request.Response[T]) error {
	if resp.StatusCode < http.StatusBadRequest {
		return nil
	}
	msg := resp.Status
	if resp.ErrorData != nil {
		msg += ": " + resp.ErrorData.Error()
	}
	return errors.New(msg)
}

// name fills in skill names, looking up the ones not stored yet.
func name(ctx context.Context, c *eveonline.Client, chars []stored) error {
	q := New(module.Database(ctx))
	rows, err := q.ListNames(ctx)
	if err != nil {
		return fmt.Errorf("reading skill names: %w", err)
	}
	names := make(map[int64]string, len(rows))
	for _, r := range rows {
		names[r.SkillID] = r.Name
	}
	var unknown []int64
	for _, ch := range chars {
		for _, s := range ch.Data.Queue {
			if _, ok := names[s.ID]; !ok && !slices.Contains(unknown, s.ID) {
				unknown = append(unknown, s.ID)
			}
		}
	}
	if len(unknown) > 0 {
		// At most 50 skills per queue, so well under ESI's 1000 IDs per request.
		resp, err := postuniversenames.Request(ctx, c, &postuniversenames.Input{Body: unknown})
		if err == nil {
			err = check(resp)
		}
		if err != nil {
			return fmt.Errorf("requesting skill names: %w", err)
		}
		for _, n := range resp.Data {
			if err := q.SaveName(ctx, SaveNameParams{SkillID: n.Id, Name: n.Name}); err != nil {
				return fmt.Errorf("saving skill name: %w", err)
			}
			names[n.Id] = n.Name
		}
	}
	for _, ch := range chars {
		for i := range ch.Data.Queue {
			ch.Data.Queue[i].Name = names[ch.Data.Queue[i].ID]
		}
	}
	return nil
}

const maxSkills = 50

type queueParams struct {
	// Skills is how many upcoming skills to return per character; default
	// 5, at most 50.
	Skills int `json:"skills"`
}

// Character is a character's queue as of now.
type Character struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Paused is a queue with skills that is not training.
	Paused bool `json:"paused"`
	// Ends is when the last skill finishes; null when the queue is empty
	// or paused.
	Ends *time.Time `json:"ends"`
	// Remaining counts the skills not finished yet.
	Remaining int `json:"remaining"`
	// Skills are the first upcoming skills, the one in training first.
	Skills []Skill `json:"skills"`
	// TotalSP and UnallocatedSP include the skills finished since the
	// character last logged in.
	TotalSP       int64      `json:"totalSp"`
	UnallocatedSP int64      `json:"unallocatedSp"`
	FetchedAt     *time.Time `json:"fetchedAt"`
	Error         string     `json:"error"`
}

// queue returns every character's queue by name. ESI keeps finished skills
// in the queue, and out of /skills, until the character logs in, and the
// stored queue ages between updates: skills that finished by now are left
// out of the queue here and added to the skill points.
func (*skills) queue(ctx context.Context, p queueParams) ([]Character, error) {
	if p.Skills < 0 || p.Skills > maxSkills {
		return nil, fmt.Errorf("skills must be 0 to %d, got %d", maxSkills, p.Skills)
	}
	n := cmp.Or(p.Skills, 5)
	var snap snapshot
	if _, err := module.LoadSnapshot(ctx, &snap); err != nil {
		return nil, err
	}
	now := module.Clock(ctx)()
	out := make([]Character, 0, len(snap.Characters))
	for _, s := range snap.Characters {
		sp := make(map[int64]int64, len(s.Data.Skills))
		for _, k := range s.Data.Skills {
			sp[k.ID] = k.SP
		}
		total := s.Data.TotalSP
		left := []Skill{}
		for _, e := range s.Data.Queue {
			if e.Finish == nil || e.Finish.After(now) {
				left = append(left, e.Skill)
				continue
			}
			if e.EndSP != nil && *e.EndSP > sp[e.ID] {
				total += *e.EndSP - sp[e.ID]
				sp[e.ID] = *e.EndSP
			}
		}
		ch := Character{
			ID: s.ID, Name: s.Name, Remaining: len(left), Skills: left[:min(n, len(left))],
			TotalSP: total, UnallocatedSP: s.Data.UnallocatedSP, FetchedAt: s.FetchedAt, Error: s.Error,
		}
		if len(left) > 0 {
			ch.Paused = left[0].Finish == nil
			if !ch.Paused {
				ch.Ends = left[len(left)-1].Finish
			}
		}
		out = append(out, ch)
	}
	return out, nil
}
