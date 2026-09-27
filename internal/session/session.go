package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"pavel_ovsyannikov/internal/domain"
)

type Store interface {
	User(context.Context, string) (domain.User, error)
	CreateSession(context.Context, string, int64, time.Time) error
	Session(context.Context, string) (domain.User, error)
	DeleteSession(context.Context, string) error
}
type Manager struct {
	Store Store
	TTL   time.Duration
}

func (m *Manager) Login(ctx context.Context, login, password string) (string, error) {
	u, e := m.Store.User(ctx, login)
	if errors.Is(e, domain.ErrNotFound) {
		return "", domain.ErrUnauthorized
	}
	if e != nil {
		return "", e
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return "", domain.ErrUnauthorized
	}
	b := make([]byte, 32)
	if _, e = rand.Read(b); e != nil {
		return "", fmt.Errorf("session entropy: %w", e)
	}
	id := hex.EncodeToString(b)
	if e = m.Store.CreateSession(ctx, id, u.ID, time.Now().Add(m.TTL)); e != nil {
		return "", e
	}
	return id, nil
}
