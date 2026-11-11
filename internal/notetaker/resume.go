package notetaker

// Notes outlive the server taking them. The note-taker runs inside a TeamUp
// server, and a server is replaced on every deploy: before this, a deploy in
// the middle of a meeting stopped the notes where they were and left them
// "being taken" for good, with nothing after that point heard and no minutes
// ever sent. Now a server renews a claim on each call it is taking notes in,
// and gives it up when it stops; any server finding notes that nobody has
// claimed for a while carries on with them: back into the call if it is still
// going, or straight to writing the minutes if it has ended.

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// beat renews this server's claim on a call's notes.
func (s *Service) beat(callID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.db.Exec(ctx, `UPDATE meeting_notes SET heartbeat_at = NOW() WHERE call_id = $1`, callID); err != nil {
		s.logger.Warn().Err(err).Str("call", callID.String()).Msg("notetaker: could not renew the claim")
	}
}

// keepAlive renews the claim until the returned func is called, for as long as
// the minutes take to write.
func (s *Service) keepAlive(callID uuid.UUID) (stop func()) {
	s.beat(callID)
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(s.Heartbeat)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.beat(callID)
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

// release gives up the claim at once, so the next server need not wait.
func (s *Service) release(callID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.db.Exec(ctx, `UPDATE meeting_notes SET heartbeat_at = '-infinity' WHERE call_id = $1 AND status = 'taking'`, callID); err != nil {
		s.logger.Warn().Err(err).Str("call", callID.String()).Msg("notetaker: could not hand the call over")
	}
}

// Resume carries on with every call's notes that nobody is working on. Each
// is claimed atomically, so with several servers sweeping only one takes it.
func (s *Service) Resume(ctx context.Context) {
	rows, err := s.db.Query(ctx, `
		UPDATE meeting_notes n SET heartbeat_at = NOW()
		FROM calls c
		WHERE c.id = n.call_id AND n.status IN ('taking', 'writing')
		  AND n.heartbeat_at < NOW() - make_interval(secs => $1)
		RETURNING n.call_id, n.org_id, n.status, c.status, c.room_name`, s.StaleAfter.Seconds())
	if err != nil {
		s.logger.Warn().Err(err).Msg("notetaker: could not look for notes left behind")
		return
	}
	type left struct {
		callID, orgID     uuid.UUID
		notes, call, room string
	}
	var found []left
	for rows.Next() {
		var l left
		if err := rows.Scan(&l.callID, &l.orgID, &l.notes, &l.call, &l.room); err != nil {
			rows.Close()
			s.logger.Warn().Err(err).Msg("notetaker: could not read notes left behind")
			return
		}
		found = append(found, l)
	}
	rows.Close()

	for _, l := range found {
		set, err := s.Settings(ctx, l.orgID)
		if err != nil {
			s.logger.Warn().Err(err).Msg("notetaker: could not read the settings")
			continue
		}
		log := s.logger.Info().Str("call", l.callID.String())
		if ready, _ := set.Ready(); ready && l.notes == "taking" && l.call == "active" {
			log.Msg("notetaker: carrying on in a call another server left")
			s.start(l.orgID, l.callID, l.room, set)
			continue
		}
		// The call has ended, or the note-taker was turned off since: what was
		// heard becomes the minutes.
		s.mu.Lock()
		stopping := s.stopping
		if !stopping {
			s.running.Add(1)
		}
		s.mu.Unlock()
		if stopping {
			s.release(l.callID)
			continue
		}
		log.Msg("notetaker: writing the minutes another server left unwritten")
		go func(l left) {
			defer s.running.Done()
			ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
			defer cancel()
			stop := s.keepAlive(l.callID)
			defer stop()
			s.compose(ctx, l.orgID, l.callID, set, "")
		}(l)
	}
}

// RunResume sweeps for notes left behind now, which is what picks up the calls
// a server handed over on its way out, and then every interval until ctx ends.
func (s *Service) RunResume(ctx context.Context, every time.Duration) {
	s.Resume(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Resume(ctx)
		}
	}
}

// Stop hands every call this server is taking notes in over to the next one:
// it stops listening, saves what it has already heard, and gives up the claim.
// Minutes being written are finished if there is time. It returns when all of
// that is done or ctx ends.
func (s *Service) Stop(ctx context.Context) {
	s.mu.Lock()
	s.stopping = true
	for _, sess := range s.sessions {
		sess.handOver()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.running.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		s.logger.Warn().Msg("notetaker: stopped before every call was handed over; another server will carry on once the claim runs out")
	}
}
