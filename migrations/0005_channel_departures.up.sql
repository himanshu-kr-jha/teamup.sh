-- Anybody who opens a public or announcement channel joins it, which meant
-- leaving one was undone the moment they touched it again. Remember that they
-- left, so the automatic join can respect the decision.
CREATE TABLE IF NOT EXISTS channel_departures (
    channel_id  UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    left_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, user_id)
);
