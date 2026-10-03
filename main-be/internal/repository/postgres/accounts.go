package postgres

import (
	"context"

	"terex/main-be/internal/domain"
)

const accountCols = "id, platform, name, url, note, bucket, file_name, connection_state,remote_account_id,connection_error,connection_checked_at,connection_scopes,credential_expires_at,connection_version,credential_ref->>'display_name'"

func (s *Store) ListAccounts(ctx context.Context) ([]domain.Account, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+accountCols+" FROM accounts ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Account{}
	for rows.Next() {
		var a domain.Account
		if err := rows.Scan(&a.ID, &a.Platform, &a.Name, &a.URL, &a.Note, &a.Bucket, &a.FileName, &a.ConnectionState, &a.RemoteAccountID, &a.ConnectionError, &a.ConnectionCheckedAt, &a.ConnectionScopes, &a.CredentialExpiresAt, &a.ConnectionVersion, &a.RemoteAccountName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAccount(ctx context.Context, id int64) (domain.Account, error) {
	var a domain.Account
	err := s.pool.QueryRow(ctx, "SELECT "+accountCols+" FROM accounts WHERE id=$1", id).
		Scan(&a.ID, &a.Platform, &a.Name, &a.URL, &a.Note, &a.Bucket, &a.FileName, &a.ConnectionState, &a.RemoteAccountID, &a.ConnectionError, &a.ConnectionCheckedAt, &a.ConnectionScopes, &a.CredentialExpiresAt, &a.ConnectionVersion, &a.RemoteAccountName)
	return a, mapErr(err)
}

func (s *Store) GetOrCreateAccount(ctx context.Context, platform, name string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO accounts(platform, name) VALUES ($1,$2)
		ON CONFLICT (platform, name) DO UPDATE SET name = EXCLUDED.name RETURNING id`, platform, name).Scan(&id)
	return id, err
}

// CreateAccount upserts by (platform, name), keeping existing fields when new ones are empty.
func (s *Store) CreateAccount(ctx context.Context, a domain.Account) (domain.Account, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO accounts(platform, name, url, note, bucket, file_name)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (platform, name) DO UPDATE SET
		  url = COALESCE(EXCLUDED.url, accounts.url),
		  note = COALESCE(EXCLUDED.note, accounts.note),
		  bucket = COALESCE(EXCLUDED.bucket, accounts.bucket),
		  file_name = COALESCE(EXCLUDED.file_name, accounts.file_name)
		RETURNING id`, a.Platform, a.Name, a.URL, a.Note, a.Bucket, a.FileName).Scan(&id)
	if err != nil {
		return domain.Account{}, err
	}
	return s.GetAccount(ctx, id)
}

func (s *Store) UpdateAccount(ctx context.Context, id int64, u domain.AccountUpdate, bucket, file *string) (domain.Account, error) {
	_, err := s.pool.Exec(ctx, `UPDATE accounts SET
		platform = COALESCE($2, platform), name = COALESCE($3, name),
		url = COALESCE($4, url), note = COALESCE($5, note),
		bucket = COALESCE($6, bucket), file_name = COALESCE($7, file_name)
		WHERE id=$1`, id, u.Platform, u.Name, u.URL, u.Note, bucket, file)
	if err != nil {
		return domain.Account{}, err
	}
	return s.GetAccount(ctx, id)
}

func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM accounts WHERE id=$1", id)
	return err
}

func (s *Store) AccountNamesByPlatform(ctx context.Context) (map[string][]string, error) {
	rows, err := s.pool.Query(ctx, "SELECT platform, name FROM accounts ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{"youtube": {}, "tiktok": {}, "instagram": {}}
	for rows.Next() {
		var p, n string
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out[p] = append(out[p], n)
	}
	return out, rows.Err()
}

// ── banners / backgrounds (same shape, table chosen by kind) ──────────────────

func table(k domain.AssetKind) string {
	if k == domain.KindBackground {
		return "backgrounds"
	}
	return "banners"
}

func (s *Store) ListAssets(ctx context.Context, kind domain.AssetKind) ([]domain.Asset, error) {
	rows, err := s.pool.Query(ctx, "SELECT id, name, note, bucket, file_name FROM "+table(kind)+" ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Asset{}
	for rows.Next() {
		var a domain.Asset
		if err := rows.Scan(&a.ID, &a.Name, &a.Note, &a.Bucket, &a.FileName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAsset(ctx context.Context, kind domain.AssetKind, id int64) (domain.Asset, error) {
	var a domain.Asset
	err := s.pool.QueryRow(ctx, "SELECT id, name, note, bucket, file_name FROM "+table(kind)+" WHERE id=$1", id).
		Scan(&a.ID, &a.Name, &a.Note, &a.Bucket, &a.FileName)
	return a, mapErr(err)
}

func (s *Store) GetOrCreateAssetByName(ctx context.Context, kind domain.AssetKind, name string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, "SELECT id FROM "+table(kind)+" WHERE name=$1", name).Scan(&id)
	if err == nil {
		return id, nil
	}
	err = s.pool.QueryRow(ctx, "INSERT INTO "+table(kind)+"(name) VALUES ($1) RETURNING id", name).Scan(&id)
	return id, err
}

func (s *Store) CreateAsset(ctx context.Context, kind domain.AssetKind, a domain.Asset) (domain.Asset, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO `+table(kind)+`(name, note, bucket, file_name) VALUES ($1,$2,$3,$4)
		ON CONFLICT (name) DO UPDATE SET note=EXCLUDED.note, bucket=EXCLUDED.bucket, file_name=EXCLUDED.file_name
		RETURNING id`, a.Name, a.Note, a.Bucket, a.FileName).Scan(&id)
	if err != nil {
		return domain.Asset{}, err
	}
	return s.GetAsset(ctx, kind, id)
}

func (s *Store) UpdateAsset(ctx context.Context, kind domain.AssetKind, id int64, u domain.AssetUpdate) (domain.Asset, error) {
	if _, err := s.pool.Exec(ctx, "UPDATE "+table(kind)+" SET name=COALESCE($2,name), note=COALESCE($3,note) WHERE id=$1", id, u.Name, u.Note); err != nil {
		return domain.Asset{}, err
	}
	return s.GetAsset(ctx, kind, id)
}

func (s *Store) DeleteAsset(ctx context.Context, kind domain.AssetKind, id int64) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM "+table(kind)+" WHERE id=$1", id)
	return err
}
