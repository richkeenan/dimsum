package control

import (
	"context"
	"strings"

	"github.com/richkeenan/dimsum/internal/config"
)

func (s *Service) PasswordHash() (string, error) {
	if s.options.Store == nil {
		return "", ErrUnavailable
	}
	b, err := s.options.Store.ActiveSecret(config.AdminSecretName)
	return strings.TrimSpace(string(b)), err
}

func (s *Service) SetPasswordHash(ctx context.Context, hash string) (Activation, error) {
	if s.options.Store == nil || s.options.Store.Snapshot() == nil {
		return Activation{}, ErrUnavailable
	}
	expected := s.options.Store.Snapshot().Revision()
	result, err := s.options.Store.SetAdminSecret(ctx, expected, []byte(hash+"\n"))
	return activation(result), err
}
