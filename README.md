# dotfile-manager

`dotfile-manager` 是一个用 Go 实现的 dotfiles 部署工具，用来把你的配置文件按声明式规则映射到各个位置，例如 `~/.config` `/etc` 等。

当前支持三种策略：

1. `symlink`
2. `recursive_symlink`
3. `copy`

项目当前面向 `Linux` 和 `darwin`。

## 特性

1. 支持多 host 配置选择。
2. 支持整体软链接和递归软链接。
3. 支持带权限和属主设置的 `copy`。
4. 支持执行前预览、确认和提权。
5. 支持状态文件，用于检测受管文件是否被修改，以及清理旧受管路径。

## 构建

直接构建二进制：

```bash
mkdir -p bin
go build -o bin/dotfile-manager ./cmd/dotfile-manager
```

直接运行源码：

```bash
go run ./cmd/dotfile-manager
```

运行测试：

```bash
go test ./...
```

## 命令行

当前支持的参数：

1. `--config`
   显式指定配置文件路径。
2. `--host`
   显式指定目标 host。
3. `--yes`
   跳过最终确认提示，直接执行。

示例：

```bash
./bin/dotfile-manager --config "$HOME/dotfile/dotfile-mgr.yaml" --host my-host
```

## 配置文件位置

如果没有显式指定 `--config`，程序会按以下顺序查找：

1. `~/.config/dotfile-manager/dotfile-mgr.yaml`
2. `~/dotfile/dotfile-mgr.yaml`

如果没有显式指定 `--host`，程序会使用当前系统 `hostname`。

## 配置示例

```yaml
version: 1
root: "$HOME/dotfile"

groups:
  config_home:
    src: "config_home"
    dest: "$HOME/.config"
    strategy: symlink
    symlink_force: false

  etc:
    src: "etc"
    dest: "/etc"
    strategy: copy
    permissions:
      owner: "root"

profiles:
  nvim:
    group: "config_home"
    path: "nvim"

  dae:
    group: "etc"
    path: "dae"
    permissions:
      owner: "root"
      file_mode: "0600"
      dir_mode: "0755"

hosts:
  default:
    enable:
      - "nvim"

  my-host:
    enable:
      - "dae"
```

更完整的字段约束和行为定义见 `SPEC.md`。

## 三种策略

### `symlink`

将 profile 对应的文件或目录整体软链接到目标位置。

### `recursive_symlink`

在目标位置创建目录结构，并对叶子文件逐个创建软链接。

### `copy`

`copy` 的行为是保守的：

1. 目标不存在：直接复制。
2. 目标存在但不是受管对象：直接报错。
3. 目标存在且是受管对象：
   先比较当前目标与状态文件记录。
4. 如果目标在上次成功 apply 后被修改：直接报错。
5. 如果目标仍处于受管干净状态，再比较源和目标。
6. 如果源和目标一致：跳过。
7. 如果源和目标不一致：允许覆盖，并显示 diff 或摘要。

## 状态文件

状态文件用于：

1. 跟踪哪些路径是受管的。
2. 检测受管 `copy` 文件是否在上次成功 apply 后被修改。
3. 支持禁用 profile 或策略切换时清理旧受管路径。

状态目录优先级：

1. `DOTFILE_MANAGER_STATE_DIR`
2. `XDG_STATE_HOME/dotfile-manager`
3. `~/.local/state/dotfile-manager`

状态文件路径格式：

```text
<state-dir>/<config-hash>/<host>.json
```

状态文件中的权限字段使用八进制字符串，例如：

```json
"mode": "0600"
```

## 提权

如果操作涉及受保护路径，例如 `/etc`，程序会在需要时通过 `sudo` 发起交互式提权。

程序不会要求你手动执行 `sudo dotfile-manager ...`，但系统中需要可用的 `sudo` 命令。

如果你的环境没有 `sudo`，涉及受保护路径时请先安装它。

## 清理行为

清理只针对状态文件中记录过的受管路径。

不会删除：

1. 未受管的额外文件
2. 未受管的额外目录

如果旧目录里存在未受管文件，程序会：

1. 保留目录
2. 给出提示
3. 在某些必须复用旧路径的迁移场景下直接中止

## 当前约束

1. 不支持源目录中的符号链接。
2. 使用 `fail-fast` 策略，遇到不可恢复错误立即停止。
3. 不做自动回滚。
4. 默认一次总确认，不逐文件确认。

## 相关文档

1. `SPEC.md`
   项目的精炼规格、约束和行为定义。
