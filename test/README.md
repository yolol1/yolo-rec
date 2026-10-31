# 测试工具目录

本目录包含用于本地开发和测试的辅助工具。

## 开发环境设置

首次克隆项目后，运行以下命令安装开发工具：

```bash
go generate ./tools/devtools.go
```

也可以分别安装：

```bash
go install github.com/go-delve/delve/cmd/dlv@latest
go install golang.org/x/tools/gopls@latest
go install honnef.co/go/tools/cmd/staticcheck@latest
```

开发工具版本通过 `go.mod` 锁定。
