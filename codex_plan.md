# dotfile-manager 实现规划

## 配置文件设计

本节定义 `dotfile-manager` 的配置文件结构、字段语义、合并规则和校验约束，作为后续实现配置解析、主机选择、执行计划生成和用户提示的依据。

### 设计目标

配置文件设计需要满足以下目标：

1. 支持将 `dotfile` 仓库中的不同分类目录映射到不同目标目录。
2. 支持多台主机使用同一份配置，并允许每台主机启用不同的配置项。
3. 支持三种部署策略：整体符号链接、递归符号链接、复制。
4. 支持仅对复制行为配置权限和属主。
5. 尽量降低日常维护成本，让最常修改的“启用哪些配置”保持结构简单。
6. 让配置结构便于 Go 中建模、校验和报错。

### 核心术语

为避免后续文档和代码中的概念混乱，先统一术语：

1. `root`
   dotfile 仓库根目录，通常为 `$HOME/dotfile`。
2. `group`
   一类映射规则。它定义源分类目录、目标基础目录以及默认部署策略。
3. `profile`
   一个可启用的具体配置项，通常对应 `root/group.src/path` 下的一个目录。
4. `host`
   一台机器的配置视图。它定义这台机器启用哪些 `profile`，以及是否有主机专属 `profile` 或覆盖项。
5. `strategy`
   将源配置部署到目标目录的方式。
6. `permissions`
   复制行为附带的权限设置。符号链接行为不使用该字段。

### 目录约定

仓库目录采用“分类目录 + profile 目录”的约定：

```text
~/dotfile/
  config_home/
    nvim/
    kitty/
  etc/
    dae/
```

例如：

1. `~/dotfile/config_home/nvim` 对应某个目标位置下的 `nvim`
2. `~/dotfile/etc/dae` 对应某个目标位置下的 `dae`

配置文件中的 `group.src` 指向分类目录名，`profile.path` 指向该分类目录下的具体 profile 目录名。

### 推荐配置结构

配置文件推荐使用 YAML，基础结构如下：

```yaml
version: 1
root: "$HOME/dotfile"

groups:
  config_home:
    src: "config_home"
    dest: "$HOME/.config"
    strategy: symlink

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
      file_mode: "0644"
      dir_mode: "0755"

hosts:
  default:
    enable:
      - "nvim"

  my-laptop:
    enable:
      - "nvim"
      - "dae"
      - "private_app"

    host_profiles:
      private_app:
        group: "config_home"
        path: "private_app"
        strategy: "recursive_symlink"

    overrides:
      nvim:
        strategy: "copy"
```

### 顶层字段

#### `version`

配置文件版本号，用于后续兼容性演进。第一版固定为：

```yaml
version: 1
```

后续如果配置格式发生不兼容调整，应增加版本号，并在程序启动时进行校验。

#### `root`

dotfile 仓库的根目录。程序应支持环境变量展开，例如 `$HOME/dotfile`。

要求：

1. 必填。
2. 必须是目录路径。
3. 程序读取配置后应先展开环境变量，再转为绝对路径。

#### `groups`

定义“分类目录如何映射到目标基础目录”。这是共享规则层，不直接表示某台机器启用了哪些配置。

每个 `group` 包含：

1. `src`
   `root` 下的分类目录名，例如 `config_home`、`etc`。
2. `dest`
   目标基础目录，例如 `$HOME/.config`、`/etc`。
3. `strategy`
   该分类目录下 profile 的默认部署策略。
4. `permissions`
   该分类目录下 profile 的默认复制权限配置，仅在 `copy` 策略下有意义。

说明：

1. `groups` 负责定义默认行为。
2. `groups` 不负责决定某个 profile 是否启用。
3. `groups` 中的 `permissions` 可以被 `profiles` 或 `hosts.*.overrides` 进一步覆盖。

#### `profiles`

定义全局共享的 profile。一个 profile 表示一个可启用配置项。

每个 `profile` 包含：

1. `group`
   引用一个已定义的 `groups` 键。
2. `path`
   `group.src` 下的目录名，例如 `nvim`、`dae`。
3. `dest`
   可选。若配置，则覆盖 `group.dest`。
4. `strategy`
   可选。若配置，则覆盖 `group.strategy`。
5. `permissions`
   可选。若配置，则覆盖 `group.permissions`。

`profiles` 负责描述“有哪些可用配置项”，不负责描述“哪台机器启用哪些项”。

#### `hosts`

定义具体主机的配置视图。

每个 host 可以包含：

1. `enable`
   当前主机启用的 profile 名称列表。
2. `host_profiles`
   仅当前主机使用的私有 profile 定义。
3. `overrides`
   对当前主机已启用 profile 的覆盖配置。

建议约定：

1. `hosts.default` 作为默认主机配置。
2. 当前机器若存在 `hosts.<hostname>`，则在 `hosts.default` 的基础上叠加该配置。
3. 若不存在 `hosts.<hostname>`，则只使用 `hosts.default`。

### `host_profiles` 与 `overrides` 的职责划分

这是配置结构中必须明确的一点：

1. `host_profiles` 用来定义“全局 `profiles` 中不存在，但当前主机需要使用”的私有 profile。
2. `overrides` 只用于覆盖已有 profile 的字段，不负责创建新 profile。

这样划分的原因是：

1. `override` 的语义应保持单一，即“修改已存在定义”。
2. 如果允许 `overrides` 同时承担新增功能，会让配置语义变得模糊，增加实现复杂度。
3. 将新增和覆盖拆开后，程序校验规则更清晰，报错也更直接。

### 策略定义

配置中支持以下三种 `strategy`：

#### `symlink`

整体符号链接。

行为定义：

1. 将 `root/group.src/path` 作为一个整体，直接链接到目标位置。
2. 目标位置最终表现为“一个符号链接目录”。
3. 不递归创建内部目录，也不逐个链接内部文件。

示例：

```text
源: ~/dotfile/config_home/nvim
目标: ~/.config/nvim
结果: ~/.config/nvim 是一个指向源目录的符号链接
```

#### `recursive_symlink`

递归符号链接。

行为定义：

1. 先在目标位置创建目录结构。
2. 遇到目录时继续递归创建子目录。
3. 遇到文件时，为该文件创建符号链接。
4. 最终效果是“目录是实际目录，叶子文件是符号链接”。

示例：

```text
源: ~/dotfile/config_home/nvim
目标: ~/.config/nvim
结果:
  ~/.config/nvim/lua/      是实际目录
  ~/.config/nvim/init.lua  是符号链接
```

适用场景：

1. 目标目录下可能还需要放置本机私有文件。
2. 不希望整个目录被单个符号链接占据。

#### `copy`

复制。

行为定义：

1. 递归复制目录和文件到目标位置。
2. 复制完成后，目标文件与源文件不再通过链接关系绑定。
3. 如配置了 `permissions`，应在复制后应用对应权限。

适用场景：

1. 目标位置不适合使用符号链接。
2. 需要设置属主和权限，例如 `/etc` 下的配置文件。

### 权限字段

`permissions` 仅对 `copy` 策略生效。推荐字段如下：

```yaml
permissions:
  owner: "root"
  file_mode: "0644"
  dir_mode: "0755"
```

字段说明：

1. `owner`
   复制后设置属主。建议在实现中支持 `user` 或 `user:group` 两种形式，具体是否第一版就实现 `group`，可在实现阶段再定。
2. `file_mode`
   普通文件权限，使用字符串表示八进制值。
3. `dir_mode`
   目录权限，使用字符串表示八进制值。

约束：

1. 如果 `strategy` 不是 `copy`，配置了 `permissions` 时应给出提示或校验错误。
2. `file_mode` 和 `dir_mode` 必须是合法的八进制权限字符串。
3. 若设置 `owner` 但当前进程权限不足，执行阶段应明确报错。

### 路径拼接规则

每个 profile 的源路径和目标路径按如下规则生成：

1. 源路径：`root + group.src + profile.path`
2. 目标路径默认：`effective_dest + basename(profile.path)`

其中：

1. `effective_dest` 优先使用 `profile.dest` 或 host override 中的 `dest`。
2. 如果没有覆盖，则使用 `group.dest`。

示例：

```yaml
group:
  src: "config_home"
  dest: "$HOME/.config"

profile:
  path: "nvim"
```

则：

1. 源路径为 `~/dotfile/config_home/nvim`
2. 目标路径为 `~/.config/nvim`

说明：

1. 当前设计中，`path` 应为相对路径，不能是绝对路径。
2. 当前设计默认一个 profile 对应一个目录；若后续要支持单文件 profile，可在未来版本扩展。

### 合并与覆盖规则

配置解析后的有效 profile 应按以下优先级生成：

1. `group` 默认值
2. `profile` 定义覆盖 `group`
3. `host_profiles` 作为主机私有 profile 直接参与启用集合
4. `hosts.<selected>.overrides` 覆盖最终启用 profile

更具体地说：

1. 对于全局 profile：先读取 `groups` 中对应组的默认配置，再应用 `profiles.<name>` 的覆盖。
2. 对于主机私有 profile：先读取其引用 `group` 的默认配置，再应用 `host_profiles.<name>` 的覆盖。
3. 若当前主机存在 `overrides.<name>`，则在上一步结果上继续覆盖。

建议实现中将这个过程输出为一个明确的“有效配置结构”，供后续执行计划生成模块使用。

### 主机选择规则

配置文件设计中建议支持以下主机选择规则：

1. 默认使用当前系统 hostname。
2. 允许命令行参数显式指定目标 host。
3. 若指定 host 不存在，应报错。
4. 若未指定 host 且当前 hostname 未配置，则退回到 `hosts.default`。

为了让行为可预测，建议第一版明确规定：

1. `hosts.default` 不是保留关键字以外的特殊结构，它本质上也是一个 host。
2. 程序内部先载入 `hosts.default`，再叠加选中的具体 host。

### 启用列表规则

`hosts.<host>.enable` 只声明当前主机启用哪些 profile 名称。

约束建议：

1. `enable` 中的名称必须存在于 `profiles` 或当前 host 的 `host_profiles` 中。
2. 名称不能重复。
3. 若 `enable` 为空，则表示该 host 不启用任何 profile。
4. `hosts.default.enable` 与具体 host 的 `enable` 合并策略需要明确。

推荐第一版采用“并集”策略：

1. 最终启用集合 = `hosts.default.enable` 与 `hosts.<selected>.enable` 的并集。
2. 若后续需要支持禁用默认项，再引入额外字段，例如 `disable`。

这个设计更适合初版，因为它简单、直观，且不容易误删默认启用项。

### 冲突处理

配置文件设计中暂不引入顶层 `defaults.force` 一类字段。

原因：

1. `force` 语义不够明确，无法准确表达“覆盖、跳过、备份”等不同策略。
2. 初版先把重点放在路径映射、策略选择、主机启用和权限上。
3. 冲突处理可以在执行阶段单独设计为命令行参数或后续配置项。

建议在后续实现文档中将冲突处理设计为独立主题，例如：

1. `skip`
2. `overwrite`
3. `backup`

但在本阶段的配置文件结构中先不加入该字段，以保持配置最小可用。

### 校验规则

程序加载配置后，建议至少执行以下校验：

1. `version` 必须受支持。
2. `root` 必填且必须存在。
3. `groups` 中每个 `src` 必须是相对路径。
4. `groups` 中每个 `dest` 必须为有效路径。
5. `profiles` 和 `host_profiles` 中引用的 `group` 必须存在。
6. `profiles.path` 和 `host_profiles.path` 必须是相对路径。
7. `strategy` 必须是 `symlink`、`recursive_symlink`、`copy` 之一。
8. 非 `copy` 策略不得携带 `permissions`，否则给出错误或强警告。
9. `hosts.default` 建议存在；若不存在，程序仍可运行，但需要定义清晰行为。
10. `enable` 中的每个 profile 名称必须可解析。
11. 同一 host 中，`host_profiles` 的名称不得与全局 `profiles` 中已存在的名称重复。

### Go 建模建议

虽然当前阶段不写代码，但配置结构设计应考虑后续 Go 建模的清晰度。建议采用以下方向：

1. 顶层配置结构体 `Config`
2. `GroupConfig`
3. `ProfileConfig`
4. `HostConfig`
5. `PermissionsConfig`
6. 配置解析后的运行时结构 `EffectiveProfile`

其中：

1. 原始结构体负责 YAML 反序列化。
2. 运行时结构体负责承载“合并后的最终结果”。
3. 路径展开、默认值填充、合法性校验应与纯粹的 YAML 解析分层处理。

### 第一版配置设计结论

当前阶段确认的配置设计结论如下：

1. 顶层保留 `version`、`root`、`groups`、`profiles`、`hosts`。
2. 暂不设置顶层 `defaults`。
3. `softlink` 统一更名为 `symlink`。
4. 递归符号链接策略命名为 `recursive_symlink`。
5. `own` 更名为 `owner`。
6. 主机专用 profile 通过 `host_profiles` 定义。
7. `overrides` 只承担覆盖职责，不承担新增职责。
8. `permissions` 仅对 `copy` 生效。
9. `hosts.default` 与具体 host 采用叠加模型。
10. `enable` 列表是日常主要维护入口，应尽量保持简单。

这套结构在初版中已经能够支持：

1. 多分类目录映射
2. 多主机启用不同配置
3. 主机专属配置项
4. 不同部署策略
5. 复制权限控制

同时，它也为后续扩展保留了空间，例如：

1. 单文件 profile
2. 冲突处理策略配置
3. 更细粒度的权限和属组控制
4. profile 依赖关系
5. 条件启用规则
