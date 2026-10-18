package postgres

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
	if _, err = tx.ExecContext(ctx, "INSERT INTO login_flows(state_hash,nonce,verifier,expires_at) VALUES($1,$2,$3,$4)", flow.Hash, flow.Nonce, flow.Verifier, flow.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ConsumeLoginFlow(ctx context.Context, hash string) (oidclogin.Flow, error) {
	var flow oidclogin.Flow
	err := s.db.QueryRowContext(ctx, "DELETE FROM login_flows WHERE state_hash=$1 AND expires_at>now() RETURNING state_hash,nonce,verifier,expires_at", hash).Scan(&flow.Hash, &flow.Nonce, &flow.Verifier, &flow.ExpiresAt)
	return flow, err
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
