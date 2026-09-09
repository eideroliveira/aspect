// Package store persists products.
package store

import "gorm.io/gorm"

// Product is a sellable item.
type Product struct {
	gorm.Model
	SKU  string `gorm:"uniqueIndex" json:"sku"`
	Name string
	note string
}

// Store wraps the database.
type Store struct{ db *gorm.DB }

// Open migrates and returns a store. Second sentence is dropped.
func Open(db *gorm.DB) (*Store, error) { return &Store{db: db}, nil }

// Find returns a product by SKU.
func (s *Store) Find(sku string) (*Product, error) { return nil, nil }

func (s *Store) helper() {}

type hidden struct{}

func (h hidden) Exported() {}
