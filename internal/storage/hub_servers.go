package storage

import (
	"context"
	"database/sql"
	"fmt"

	"local-agent-workbench/internal/servers"
)

func (s *SQLite) SaveServerProfile(ctx context.Context, profile servers.Profile) error {
	var probed any
	if profile.LastProbeAt != nil {
		probed = formatTime(*profile.LastProbeAt)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO server_profiles(id,display_name,host,port,username,auth_method,private_key_path,secret_ref,default_remote_path,status,last_error,last_probe_at,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name, host=excluded.host, port=excluded.port, username=excluded.username,
  auth_method=excluded.auth_method, private_key_path=excluded.private_key_path, secret_ref=excluded.secret_ref,
  default_remote_path=excluded.default_remote_path, status=excluded.status, last_error=excluded.last_error,
  last_probe_at=excluded.last_probe_at, updated_at=excluded.updated_at`,
		profile.ID, profile.DisplayName, profile.Host, profile.Port, profile.User, profile.AuthMethod, profile.PrivateKeyPath,
		profile.SecretRef, profile.DefaultRemotePath, profile.Status, profile.LastError, probed,
		formatTime(profile.CreatedAt), formatTime(profile.UpdatedAt))
	return err
}

func (s *SQLite) ListServerProfiles(ctx context.Context) ([]servers.Profile, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id,display_name,host,port,username,auth_method,private_key_path,secret_ref,default_remote_path,status,last_error,last_probe_at,created_at,updated_at
FROM server_profiles ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []servers.Profile
	for rows.Next() {
		profile, scanErr := scanServerProfile(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, profile)
	}
	return result, rows.Err()
}

func (s *SQLite) GetServerProfile(ctx context.Context, id string) (servers.Profile, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id,display_name,host,port,username,auth_method,private_key_path,secret_ref,default_remote_path,status,last_error,last_probe_at,created_at,updated_at
FROM server_profiles WHERE id=?`, id)
	profile, err := scanServerProfile(row)
	if err == sql.ErrNoRows {
		return servers.Profile{}, fmt.Errorf("SSH-профиль %s не найден", id)
	}
	return profile, err
}

func (s *SQLite) DeleteServerProfile(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM server_profiles WHERE id=?`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("SSH-профиль %s не найден", id)
	}
	return nil
}

type serverProfileScanner interface {
	Scan(dest ...any) error
}

func scanServerProfile(row serverProfileScanner) (servers.Profile, error) {
	var profile servers.Profile
	var created, updated string
	var probed sql.NullString
	if err := row.Scan(&profile.ID, &profile.DisplayName, &profile.Host, &profile.Port, &profile.User, &profile.AuthMethod,
		&profile.PrivateKeyPath, &profile.SecretRef, &profile.DefaultRemotePath, &profile.Status, &profile.LastError,
		&probed, &created, &updated); err != nil {
		return servers.Profile{}, err
	}
	profile.CreatedAt, profile.UpdatedAt = parseTime(created), parseTime(updated)
	if probed.Valid && probed.String != "" {
		t := parseTime(probed.String)
		profile.LastProbeAt = &t
	}
	return profile, nil
}
