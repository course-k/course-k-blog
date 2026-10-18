package mysql

import (
	"context"

	"coursek/blog/backend/internal/oidclogin"
)

func (s *Store) PutLoginFlow(ctx context.Context, flow oidclogin.Flow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM login_flows WHERE expires_at<=now()"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO login_flows(state_hash,nonce,verifier,expires_at) VALUES(?,?,?,?)", flow.Hash, flow.Nonce, flow.Verifier, flow.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ConsumeLoginFlow(ctx context.Context, hash string) (oidclogin.Flow, error) {
	var flow oidclogin.Flow
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return flow, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, "SELECT state_hash,nonce,verifier,expires_at FROM login_flows WHERE state_hash=? AND expires_at>NOW(6) FOR UPDATE", hash).Scan(&flow.Hash, &flow.Nonce, &flow.Verifier, &flow.ExpiresAt)
	if err != nil {
		return flow, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM login_flows WHERE state_hash=?", hash); err != nil {
		return flow, err
	}
	return flow, tx.Commit()
}

func (s *Store) RevokeSessions(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM login_flows"); err != nil {
		return err
	}
	return tx.Commit()
}
