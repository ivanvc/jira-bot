package common

import "os"

type Config struct {
	ListenHTTP string
}

func LoadConfig() Config {
	return Config{
		ListenHTTP: envOrDefault("JIRA_BOT_LISTEN_HTTP", ":8080"),
	}
}

func envOrDefault(variable, fallback string) string {
	if v, ok := os.LookupEnv(variable); ok {
		return v
	}
	return fallback
}
