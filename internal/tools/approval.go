package tools

import (
	"context"
	"path/filepath"
	"strings"
)

// ApprovalRequest descreve uma ação que precisa de confirmação humana.
type ApprovalRequest struct {
	Tool    string `json:"tool"`
	Action  string `json:"action"`  // resumo legível ("write_file .env (120 bytes)")
	Reason  string `json:"reason"`  // por que a ação foi considerada sensível
	Details string `json:"details"` // conteúdo/comando completo para inspeção
}

// Approver decide se uma ação sensível pode prosseguir. A CLI implementa via
// stdin; a camada JSON-RPC delega ao cliente (UI desktop).
type Approver interface {
	Approve(ctx context.Context, req ApprovalRequest) (bool, error)
}

// ApproverFunc adapta uma função para a interface Approver.
type ApproverFunc func(ctx context.Context, req ApprovalRequest) (bool, error)

func (f ApproverFunc) Approve(ctx context.Context, req ApprovalRequest) (bool, error) {
	return f(ctx, req)
}

// AutoApprove aprova tudo (útil em testes ou com --yes).
var AutoApprove = ApproverFunc(func(context.Context, ApprovalRequest) (bool, error) { return true, nil })

// DenyAll nega tudo.
var DenyAll = ApproverFunc(func(context.Context, ApprovalRequest) (bool, error) { return false, nil })

// Policy define quais ações exigem confirmação.
type Policy struct {
	// ConfirmAllWrites exige confirmação para qualquer escrita (não só as sensíveis).
	ConfirmAllWrites bool
	// SafeCommands são executáveis que rodam sem confirmação (ex.: "go", "npm").
	// Mesmo assim, subcomandos destrutivos conhecidos pedem confirmação.
	SafeCommands []string
}

// DefaultPolicy é conservadora: comandos de build/teste/leitura liberados,
// todo o resto pede confirmação.
func DefaultPolicy() Policy {
	return Policy{
		SafeCommands: []string{
			"go", "gofmt", "golangci-lint", "npm", "npx", "pnpm", "yarn", "node",
			"python", "python3", "pytest", "cargo", "make", "ls", "cat", "grep",
			"rg", "find", "head", "tail", "wc", "git",
		},
	}
}

var sensitiveNames = []string{
	".env", ".npmrc", ".pypirc", ".netrc", "id_rsa", "id_ed25519", "credentials",
	"go.mod", "go.sum", "package.json", "package-lock.json", "Makefile",
	"Dockerfile", "docker-compose.yml",
}

var sensitiveExts = []string{".pem", ".key", ".p12", ".pfx", ".crt", ".sh"}

var sensitiveDirs = []string{".git", ".github", ".gitlab", ".circleci", ".ssh", ".vscode"}

// WriteSensitivity retorna um motivo não vazio quando a escrita em rel
// (caminho relativo à raiz) deve ser confirmada.
func (p Policy) WriteSensitivity(rel string, exists bool) string {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	for _, d := range sensitiveDirs {
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return "diretório sensível (" + d + ")"
		}
	}
	for _, n := range sensitiveNames {
		if base == n || strings.HasPrefix(base, n+".") {
			return "arquivo sensível (" + base + ")"
		}
	}
	for _, e := range sensitiveExts {
		if strings.HasSuffix(base, e) {
			return "extensão sensível (" + e + ")"
		}
	}
	if p.ConfirmAllWrites {
		if exists {
			return "sobrescrita de arquivo existente"
		}
		return "criação de arquivo"
	}
	return ""
}

var destructiveCommands = map[string]bool{
	"rm": true, "rmdir": true, "mv": true, "dd": true, "mkfs": true, "chmod": true,
	"chown": true, "sudo": true, "su": true, "curl": true, "wget": true, "ssh": true,
	"scp": true, "kill": true, "pkill": true, "shutdown": true, "reboot": true,
	"sh": true, "bash": true, "zsh": true, "eval": true,
}

// Apenas subcomandos git somente-leitura rodam sem confirmação. Opções
// globais (ex.: "-c alias.x=!cmd") também caem na confirmação.
var readOnlyGitSubcommands = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "ls-files": true,
	"rev-parse": true, "blame": true, "grep": true,
}

// Flags que transformam utilitários de leitura em execução/remoção arbitrária.
var dangerousFlags = map[string]bool{
	"--force": true, "-f": true, "-rf": true, "--hard": true,
	"-delete": true, "-exec": true, "-execdir": true, "-ok": true, "--pre": true,
	"--ext-diff": true, "--output": true,
}

// CommandRisk retorna um motivo não vazio quando o comando deve ser confirmado.
func (p Policy) CommandRisk(command string, args []string) string {
	bin := filepath.Base(command)
	if command != bin {
		return "executável referenciado por caminho"
	}
	if destructiveCommands[bin] {
		return "comando potencialmente destrutivo (" + bin + ")"
	}
	if bin == "git" && (len(args) == 0 || !readOnlyGitSubcommands[args[0]]) {
		sub := "(nenhum)"
		if len(args) > 0 {
			sub = args[0]
		}
		return "subcomando git que pode alterar estado (" + sub + ")"
	}
	for _, a := range args {
		flag, _, _ := strings.Cut(a, "=")
		if dangerousFlags[flag] {
			return "flag potencialmente destrutiva (" + flag + ")"
		}
	}
	for _, s := range p.SafeCommands {
		if s == bin {
			return ""
		}
	}
	return "comando fora da lista segura (" + bin + ")"
}
