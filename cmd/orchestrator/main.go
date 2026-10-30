// Comando orchestrator: CLI do orquestrador multi-agente.
package main

import (
	"os"

	"orchestrator/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
