// Package config carrega a configuração a partir de variáveis de ambiente
// (opcionalmente de um arquivo .env no diretório de trabalho).
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// Config é a configuração do processo.
type Config struct {
	Provider         string        // anthropic | mock
	APIKey           string        // ANTHROPIC_API_KEY (opcional se `ant auth login` foi feito)
	BaseURL          string        // ANTHROPIC_BASE_URL
	Model            string        // ORCH_MODEL
	Effort           string        // ORCH_EFFORT
	Fallbacks        bool          // ORCH_FALLBACKS
	MaxTokens        int64         // ORCH_MAX_TOKENS
	MaxStepsPerPhase int           // ORCH_MAX_STEPS
	MaxReviewRounds  int           // ORCH_MAX_REVIEW_ROUNDS
	CommandTimeout   time.Duration // ORCH_COMMAND_TIMEOUT
	ConfirmAllWrites bool          // ORCH_CONFIRM_ALL_WRITES
}

// Load lê envFile (se existir; nunca sobrescreve variáveis já definidas) e o ambiente.
func Load(envFile string) (*Config, error) {
	if envFile != "" {
		if err := godotenv.Load(envFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("lendo %s: %w", envFile, err)
		}
	}
	c := &Config{
		Provider: get("ORCH_PROVIDER", "anthropic"),
		APIKey:   os.Getenv("ANTHROPIC_API_KEY"),
		BaseURL:  os.Getenv("ANTHROPIC_BASE_URL"),
		Model:    get("ORCH_MODEL", "claude-opus-5-5"),
		Effort:   get("ORCH_EFFORT", "high"),
	}
	var err error
	if c.Fallbacks, err = getBool("ORCH_FALLBACKS", true); err != nil {
		return nil, err
	}
	if c.ConfirmAllWrites, err = getBool("ORCH_CONFIRM_ALL_WRITES", false); err != nil {
		return nil, err
	}
	maxTokens, err := getInt("ORCH_MAX_TOKENS", 64000)
	if err != nil {
		return nil, err
	}
	c.MaxTokens = int64(maxTokens)
	if c.MaxStepsPerPhase, err = getInt("ORCH_MAX_STEPS", 40); err != nil {
		return nil, err
	}
	if c.MaxReviewRounds, err = getInt("ORCH_MAX_REVIEW_ROUNDS", 1); err != nil {
		return nil, err
	}
	if c.CommandTimeout, err = time.ParseDuration(get("ORCH_COMMAND_TIMEOUT", "2m")); err != nil {
		return nil, fmt.Errorf("ORCH_COMMAND_TIMEOUT: %w", err)
	}
	return c, nil
}

func get(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getBool(k string, def bool) (bool, error) {
	v := os.Getenv(k)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", k, err)
	}
	return b, nil
}

func getInt(k string, def int) (int, error) {
	v := os.Getenv(k)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", k, err)
	}
	return n, nil
}
