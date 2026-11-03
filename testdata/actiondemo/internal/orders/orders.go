// Package orders holds order business logic.
package orders

import (
	"example.com/shop/internal/orders/repo"
	"example.com/shop/internal/payments"
	"example.com/shop/internal/store"
)

type Service struct {
	db   *store.DB
	repo *repo.Repo
}

func New(db *store.DB) *Service { return &Service{db: db, repo: repo.New()} }

func (s *Service) Count() int { return s.repo.Count() }

func (s *Service) Fulfil(id string) { payments.Charge(id) }
