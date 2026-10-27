package web

import "cooking/internal/config"

func cfgFor(base string) *config.Config { return &config.Config{BaseURL: base} }
