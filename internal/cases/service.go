package cases

import "context"

type service struct {
	// repo
}

func (s *service) ListCases(ctx context.Context) error {
	return nil
}

func NewService() *service {
	return &service{}
}
