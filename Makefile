.PHONY: build test vet demo

build:
	go build -o bin/orchestrator ./cmd/orchestrator

test:
	go test -race ./...

vet:
	go vet ./...

# Executa o pipeline completo offline (LLM roteirizado) num repo temporário.
demo: build
	@d=$$(mktemp -d) && cd $$d && git init -q && echo "# demo" > README.md && \
	  git add . && git -c user.name=demo -c user.email=demo@local commit -qm init && cd - >/dev/null && \
	  ./bin/orchestrator --repo $$d --task "crie um arquivo de demonstração" --provider mock && \
	  echo "\nRepo de demo: $$d"
