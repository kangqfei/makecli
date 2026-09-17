MODULE  := github.com/qfeius/makecli
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "DEV")
DATE    := $(shell date -u +%Y-%m-%d)
LOCAL_BIN ?= $(HOME)/.local/bin

LDFLAGS := -s -w \
	-X $(MODULE)/internal/build.Version=$(VERSION) \
	-X $(MODULE)/internal/build.Date=$(DATE)

.PHONY: sync build local test vet lint geb clean

# 把 skills submodule（skills/ → qfeius/make-platform-skills）拉齐到超项目钉住的 commit，幂等；
# 其内容经根包 embed.go 的 go:embed 编译进二进制，故所有编译类目标都以 sync 为前置。
# 升级上游由 /ship Step 0 在发版前自动 bump 并提交（.gitmodules 跟踪 main）；手动：git submodule update --remote 后提交新指针。
sync:
	git submodule update --init --recursive

build: sync
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/makecli .

local: build
	mkdir -p $(LOCAL_BIN)
	install -m 0755 bin/makecli $(LOCAL_BIN)/makecli

test: sync
	go test ./...
	node --test "npm/*.test.js"

vet: sync
	go vet ./...

lint: sync
	golangci-lint run ./...

# GEB 分形文档一致性检查；排除 vendored 的 deps/ 与 skills submodule（geb 不读 .gitignore，须显式 --exclude）
geb:
	geb lint . --exclude deps --exclude skills

clean:
	rm -rf bin/
