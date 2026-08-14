// Package service is a tiny sample app used to try GraphSentry without
// pointing it at a real (possibly private) repository.
package service

import "fmt"

// UserRepository persists users. In this toy example it just logs.
type UserRepository struct{}

// Save writes a user to storage.
func (r *UserRepository) Save(name string) error {
	fmt.Println("saving", name)
	return nil
}

// IdentityService issues identity tokens for new users.
type IdentityService struct{}

// IssueToken creates an auth token for a newly created user.
func (i *IdentityService) IssueToken(name string) string {
	return "token-for-" + name
}

// UserService coordinates user creation across the repository and identity
// service.
type UserService struct {
	repo     *UserRepository
	identity *IdentityService
}

// CreateUser validates and persists a new user, then issues them a token.
func (s *UserService) CreateUser(name string) error {
	if err := s.validate(name); err != nil {
		return err
	}
	if err := s.repo.Save(name); err != nil {
		return err
	}
	s.identity.IssueToken(name)
	return nil
}

func (s *UserService) validate(name string) error {
	if name == "" {
		return fmt.Errorf("name required")
	}
	return nil
}
