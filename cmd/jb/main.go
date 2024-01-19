package main

import (
	"github.com/ivanvc/jira-bot/internal/common"
	"github.com/ivanvc/jira-bot/internal/http"
)

func main() {
	common := &common.State{
		Config: common.LoadConfig(),
	}

	s := http.NewServer(common)
	if err := s.Start(); err != nil {
		panic(err)
	}
}
