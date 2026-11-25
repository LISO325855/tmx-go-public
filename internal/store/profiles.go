package store

import (
	"database/sql"
	"encoding/json"
	"slices"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Profiles are stored as JSON (the settings change more often than the schema should).

// ListProfiles returns every stored profile, in slug order.
func (s *Store) ListProfiles() ([]model.Profile, error) {
	rows, err := s.db.Query(`SELECT data, created_at, updated_at FROM profiles ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Profile
	for rows.Next() {
		var data string
		var p model.Profile
		var created, updated int64
		if err := rows.Scan(&data, &created, &updated); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return nil, err
		}
		p.CreatedAt, p.UpdatedAt = created, updated
		out = append(out, p)
	}
	return out, rows.Err()
}

// PutProfile inserts or replaces a profile (by slug).
func (s *Store) PutProfile(p model.Profile) error {
	p.BuiltIn, p.Modified = false, false
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO profiles (slug, data, created_at, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		p.Slug, string(b), p.CreatedAt, p.UpdatedAt)
	return err
}

// DeleteProfile removes a profile.
func (s *Store) DeleteProfile(slug string) error {
	_, err := s.db.Exec(`DELETE FROM profiles WHERE slug = ?`, slug)
	return err
}

// UpgradeFableAllowed adds Fable (model.FableModel) to the allowed models of the stored head,
// lead and worker profiles, once per store (the owner #11, 17 Nov 2026: Fable was missing from the
// model choices). A new install gets it from the defaults; this is for a store that was seeded
// before. Only a profile whose allowed models are still exactly the earlier defaults is
// changed: an owner who chose another list keeps it, and one who removes Fable afterwards is
// not given it back (the marker and the writes commit together). The default model of a
// profile is never changed.
func (s *Store) UpgradeFableAllowed() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT OR IGNORE INTO profile_default_upgrades(name) VALUES ('fable-allowed-2026-11')`)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return tx.Commit()
	}
	for _, slug := range []string{"head", "lead", "worker"} {
		var data string
		err := tx.QueryRow(`SELECT data FROM profiles WHERE slug = ?`, slug).Scan(&data)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		var current model.Profile
		if err := json.Unmarshal([]byte(data), &current); err != nil {
			return err
		}
		updated, _ := model.DefaultProfile(slug)
		// the defaults before Fable: today's list without it
		previous := slices.DeleteFunc(slices.Clone(updated.AllowedModels), func(m string) bool { return m == model.FableModel })
		if !slices.Equal(current.AllowedModels, previous) {
			continue
		}
		current.AllowedModels = slices.Clone(updated.AllowedModels)
		current.UpdatedAt = model.Now()
		b, err := json.Marshal(current)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE profiles SET data = ?, updated_at = ? WHERE slug = ?`, string(b), current.UpdatedAt, slug); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpgradeModelDefaults changes only untouched pre-ladder defaults, once per store.
// The marker and profile writes commit together, so an owner can later restore
// Sonnet-only settings without the next daemon start changing them again.
func (s *Store) UpgradeModelDefaults() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT OR IGNORE INTO profile_default_upgrades(name) VALUES ('model-ladder-2026-11')`)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return tx.Commit()
	}
	for _, slug := range []string{"lead", "worker", "reviewer"} {
		var data string
		err := tx.QueryRow(`SELECT data FROM profiles WHERE slug = ?`, slug).Scan(&data)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		var current model.Profile
		if err := json.Unmarshal([]byte(data), &current); err != nil {
			return err
		}
		updated, _ := model.DefaultProfile(slug)
		previous := updated.Clone()
		previous.Model = "claude-sonnet-5-5"
		previous.AllowedModels = []string{"claude-sonnet-5-5"}
		if !current.SameSettings(previous) {
			continue
		}
		current.Model, current.AllowedModels = updated.Model, updated.AllowedModels
		current.UpdatedAt = model.Now()
		b, err := json.Marshal(current)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE profiles SET data = ?, updated_at = ? WHERE slug = ?`, string(b), current.UpdatedAt, slug); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpgradeFreeWorker seeds the built-in profile and grants it alongside the existing
// worker permission. The marker preserves later edits to the head and lead launch lists.
func (s *Store) UpgradeFreeWorker() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("INSERT OR IGNORE INTO profile_default_upgrades(name) VALUES ('fae-free-worker')")
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return tx.Commit()
	}
	p, _ := model.DefaultProfile(model.FreeWorkerProfile)
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	now := model.Now()
	result, err = tx.Exec("INSERT OR IGNORE INTO profiles (slug, data, created_at, updated_at) VALUES (?, ?, ?, ?)",
		p.Slug, string(data), now, now)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 1 {
		_, err = tx.Exec(`UPDATE profiles
			SET data = json_insert(data, '$.can_launch[#]', ?), updated_at = ?
			WHERE slug IN ('head', 'lead')
			AND EXISTS (SELECT 1 FROM json_each(data, '$.can_launch') WHERE value = 'worker')
			AND NOT EXISTS (SELECT 1 FROM json_each(data, '$.can_launch') WHERE value = ?)`,
			p.Slug, now, p.Slug)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
