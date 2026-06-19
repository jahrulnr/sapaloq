package cursor

import (
	"github.com/jahrulnr/sapaloq/internal/bridge"
	"github.com/jahrulnr/sapaloq/internal/config"
)

func Register(reg *bridge.Registry, cfg config.Config) error {
	b, err := New(cfg)
	if err != nil {
		return err
	}
	reg.Register(b)
	return nil
}
