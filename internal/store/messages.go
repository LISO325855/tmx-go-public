package store

import (
	"database/sql"
	"errors"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

const messageCols = `id, from_id, to_id, body, state, reply_to, created_at, delivered_at, thread_id, retry_id, retry_hash, receipt, receipt_at, urgent`

func scanMessage(r scanner) (model.Message, error) {
	var m model.Message
	var reply sql.NullString
	err := r.Scan(&m.ID, &m.From, &m.To, &m.Body, &m.State, &reply, &m.CreatedAt, &m.DeliveredAt, &m.ThreadID, &m.RetryID, &m.RetryHash, &m.Receipt, &m.ReceiptAt, &m.Urgent)
	m.ReplyTo = reply.String
	return m, err
}

func (s *Store) PutMessage(m model.Message) error {
	_, err := s.db.Exec(upsert("messages", messageCols),
		m.ID, m.From, m.To, m.Body, m.State, nullable(m.ReplyTo), m.CreatedAt, m.DeliveredAt, m.ThreadID, m.RetryID, m.RetryHash, m.Receipt, m.ReceiptAt, m.Urgent)
	return err
}

// MessageByRetry is scoped to the authenticated sender, never a client-supplied identity.
func (s *Store) MessageByRetry(from, key string) (model.Message, error) {
	m, err := scanMessage(s.db.QueryRow(`SELECT `+messageCols+` FROM messages WHERE from_id=? AND retry_id=?`, from, key))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return m, err
}

func (s *Store) SetMessageReceipt(id, receipt string, at int64) error {
	_, err := s.db.Exec(`UPDATE messages SET receipt=?, receipt_at=?,
 state=CASE WHEN state='read' THEN state WHEN ? IN ('submitted','accepted','read') THEN 'delivered' WHEN ? IN ('dispatching','menu_wait','unconfirmed','stalled') THEN 'sending' ELSE state END,
 delivered_at=CASE WHEN state='read' THEN delivered_at WHEN ? IN ('submitted','accepted') THEN CASE WHEN delivered_at>0 THEN delivered_at ELSE ? END WHEN ? IN ('dispatching','menu_wait','unconfirmed','stalled') THEN 0 ELSE delivered_at END
 WHERE id=?`, receipt, at, receipt, receipt, receipt, at, receipt, id)
	return err
}

// ClaimMail commits the no-replay boundary before any runner can receive bytes.
func (s *Store) ClaimMail(ids []string, at int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		res, err := tx.Exec(`UPDATE messages SET state='sending', delivered_at=0, receipt='dispatching', receipt_at=? WHERE id=? AND state='queued'`, at, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("mail is no longer queued")
		}
	}
	return tx.Commit()
}

// A menu wait proves no paste occurred, so it is replayable. Other in-flight
// writes remain uncertain after a daemon restart.
func (s *Store) RecoverMailReceipts() error {
	if _, err := s.RequeueMenuMail(""); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE messages SET state='sending', delivered_at=0, receipt='unconfirmed', receipt_at=? WHERE receipt='dispatching' AND state<>'read'`, model.Now())
	return err
}

// RequeueMenuMail restores unpasted messages when their in-memory runner queue
// disappears. Empty to recovers all recipients during daemon startup.
func (s *Store) RequeueMenuMail(to string) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM messages WHERE receipt='menu_wait' AND state IN ('sending','delivered') AND (?='' OR to_id=?) ORDER BY created_at, rowid`, to, to)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE messages SET state='queued',delivered_at=0,receipt='queued',receipt_at=? WHERE id=? AND receipt='menu_wait'`, model.Now(), id); err != nil {
			return nil, err
		}
	}
	return ids, tx.Commit()
}

func (s *Store) PendingMailReceipts(to string) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM messages WHERE to_id=? AND receipt='dispatching'`, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) GetMessage(id string) (model.Message, error) {
	m, err := scanMessage(s.db.QueryRow(`SELECT `+messageCols+` FROM messages WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// SetMessageState updates state (and delivered_at when non-zero).
func (s *Store) SetMessageState(id string, st model.MessageState, deliveredAt int64) error {
	_, err := s.db.Exec(`UPDATE messages SET state = ?,
		delivered_at = CASE WHEN ? > 0 THEN ? ELSE delivered_at END WHERE id = ?`,
		st, deliveredAt, deliveredAt, id)
	return err
}

// MessageFilter selects messages for ListMessages. Empty fields match everything.
type MessageFilter struct {
	Party            string // from OR to this ID
	From             string
	To               string
	BodyPrefix       string // the body starts with this (e.g. a label)
	State            model.MessageState
	Limit            int // 0 = 200
	DeliveryPriority bool
	Oldest           bool // take the oldest Limit (delivery FIFO), not the newest
}

// ListMessages returns matching messages, oldest first (the newest Limit of them).
func (s *Store) ListMessages(f MessageFilter) ([]model.Message, error) {
	q := `SELECT ` + messageCols + `, rowid AS rid FROM messages WHERE 1=1`
	var args []any
	if f.Party != "" {
		q += ` AND (from_id = ? OR to_id = ?)`
		args = append(args, f.Party, f.Party)
	}
	if f.From != "" {
		q += ` AND from_id = ?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND to_id = ?`
		args = append(args, f.To)
	}
	if f.BodyPrefix != "" {
		q += ` AND substr(body, 1, ?) = ?`
		args = append(args, len([]rune(f.BodyPrefix)), f.BodyPrefix)
	}
	if f.State != "" {
		q += ` AND state = ?`
		args = append(args, f.State)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	order := "created_at DESC, rid DESC"
	if f.Oldest {
		order = "created_at, rid"
	}
	if f.DeliveryPriority {
		order = "CASE WHEN from_id='owner' OR from_id LIKE 'fleet:operator@%' THEN 0 WHEN urgent=1 THEN 1 ELSE 2 END, created_at, rid"
		q += " ORDER BY " + order + " LIMIT ?"
		q = `SELECT ` + messageCols + ` FROM (` + q + `) ORDER BY ` + order
	} else {
		q = `SELECT ` + messageCols + ` FROM (` + q + ` ORDER BY ` + order + ` LIMIT ?) ORDER BY created_at, rid`
	}
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountQueued counts messages waiting for delivery to an agent.
func (s *Store) CountQueued(to string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE to_id = ? AND state = ?`, to, model.MsgQueued).Scan(&n)
	return n, err
}

// CountSent counts messages from one party to another created at or after since (ms).
func (s *Store) CountSent(from, to string, since int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE from_id = ? AND to_id = ? AND created_at >= ?`,
		from, to, since).Scan(&n)
	return n, err
}

// RequeueUnconsumedMail undoes ClaimMail for mail to one recipient that was claimed at or after
// since and never settled (still dispatching, accepted, submitted or unconfirmed): it is queued
// again, to be delivered once more. ClaimMail is the no-replay boundary, so this is only for
// the one case where it is proven that the recipient's program consumed nothing: the harness
// refused the conversation those messages were written to (fleet resume_fallback.go). skip
// leaves a message as it is (a task receipt has its own, durable outcome). It returns the ids
// that were queued again.
func (s *Store) RequeueUnconsumedMail(to string, since, at int64, skip func(id string) bool) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM messages WHERE to_id=? AND state IN ('sending','delivered') AND (CASE WHEN delivered_at>0 THEN delivered_at ELSE receipt_at END)>=?
		AND receipt IN ('dispatching','accepted','submitted','unconfirmed','menu_wait') ORDER BY created_at`, to, since)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var done []string
	for _, id := range ids {
		if skip != nil && skip(id) {
			continue
		}
		res, err := tx.Exec(`UPDATE messages SET state='queued', delivered_at=0, receipt='queued', receipt_at=? WHERE id=? AND state IN ('sending','delivered')`, at, id)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			done = append(done, id)
		}
	}
	return done, tx.Commit()
}

// OverdueOwnerDispatches finds claimed owner sends without a submission receipt.
// A stalled receipt is durable and is not selected again or automatically replayed.
func (s *Store) OverdueOwnerDispatches(before int64) ([]model.Message, error) {
	rows, err := s.db.Query(`SELECT `+messageCols+` FROM messages WHERE from_id='owner' AND state <> 'read' AND receipt='dispatching' AND receipt_at < ? ORDER BY receipt_at`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
