package web

import "github.com/liorokman/ninjarecipe/internal/config"

func cfgFor(base string) *config.Config { return &config.Config{BaseURL: base} }
