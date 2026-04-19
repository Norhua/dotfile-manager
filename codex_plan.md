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

## 配置加载与解析流程设计

本节定义 `dotfile-manager` 在启动后如何定位配置文件、加载 YAML、选择目标 host、合并配置并生成运行时有效结果。

这一阶段的目标不是直接执行文件操作，而是产出一份结构稳定、字段完整、语义明确的运行时配置，为后续执行计划生成和实际部署提供输入。

### 设计目标

配置加载与解析流程需要满足以下目标：

1. 在 `Linux` 和 `darwin` 上都具备一致、可预测的行为。
2. 支持命令行显式指定配置文件路径和目标 host。
3. 在未显式指定配置文件时，按约定的默认路径顺序查找配置。
4. 在未显式指定 host 时，严格使用当前系统 `hostname` 选择配置，不进行模糊回退。
5. 正确处理 `hosts.default` 与具体 host 的继承关系。
6. 在真正执行前，就能判断哪些 profile 最终会被启用、使用何种策略、是否可能需要更高权限。
7. 将“配置解析”和“执行文件变更”解耦，降低实现复杂度并改善错误提示。

### 解析阶段的输入与输出

解析阶段的输入包括：

1. 配置文件路径，可能来自命令行，也可能来自默认查找路径。
2. 目标 host，可能来自命令行，也可能来自当前系统 `hostname`。
3. 当前操作系统信息，用于后续路径行为和提权能力判断。

解析阶段的输出应至少包括：

1. 被选中的配置文件绝对路径。
2. 被选中的 host 名称。
3. 合并后的 host 视图。
4. 一组可直接用于规划执行步骤的 `EffectiveProfile`。
5. 与执行相关的元信息，例如是否可能需要提权、哪些 profile 采用 `copy`、哪些 profile 存在覆盖风险。

### 总体流程

推荐将配置加载与解析分为以下阶段：

1. 定位配置文件。
2. 读取并反序列化 YAML。
3. 执行基础结构校验。
4. 进行路径与字段规范化。
5. 选择目标 host。
6. 合并 `hosts.default` 与目标 host。
7. 解析最终启用集合。
8. 合并生成 `EffectiveProfile`。
9. 输出供执行计划阶段使用的预检元信息。

这几个阶段应尽量保持单向数据流，避免在后续阶段回头修改前一阶段的原始数据。

### 配置文件定位

程序应支持以下配置文件来源：

1. 命令行参数显式指定的配置文件路径。
2. 默认配置文件搜索路径。

默认配置文件名固定为：

```text
dotfile-mgr.yaml
```

默认搜索路径按以下顺序依次查找：

1. `~/.config/dotfile-manager/dotfile-mgr.yaml`
2. `~/profile/dotfile-mgr.yaml`

规则定义：

1. 如果命令行显式指定配置文件路径，则只使用该路径，不再查找默认路径。
2. 如果未显式指定，则按顺序检查默认路径，使用第一个存在的配置文件。
3. 如果两个默认路径都不存在，则报错。
4. 如果显式指定的路径不存在，也应直接报错。

说明：

1. 该查找顺序优先遵循常见的 XDG 风格目录。
2. 程序内部应在尽早阶段将最终选择的配置文件路径转为绝对路径。

### 原始加载阶段

定位到配置文件后，程序进入原始加载阶段。

本阶段职责：

1. 读取文件内容。
2. 将 YAML 反序列化到原始配置结构体。
3. 保留尽可能接近用户输入的字段形式，供后续校验和规范化使用。

建议在实现上使用一组“原始配置结构”，例如：

1. `RawConfig`
2. `RawGroupConfig`
3. `RawProfileConfig`
4. `RawHostConfig`

这些结构只负责承载 YAML 内容，不应承担过多业务逻辑。

### 基础结构校验

YAML 反序列化成功后，先进行不依赖 host 选择的基础结构校验。

该阶段建议检查：

1. `version` 是否存在且为受支持版本。
2. `root` 是否存在且为非空字符串。
3. `groups`、`profiles`、`hosts` 是否为合法映射结构。
4. `strategy` 是否属于 `symlink`、`recursive_symlink`、`copy`。
5. `profiles` 与 `host_profiles` 中引用的 `group` 是否存在。
6. `path`、`src` 是否为相对路径。
7. `permissions` 是否仅出现在 `copy` 语义下。

这一阶段主要解决“配置文件在结构上是否合法”的问题。

### 规范化阶段

基础校验通过后，需要将原始配置转换为内部统一格式。

规范化阶段建议完成以下工作：

1. 展开支持环境变量的路径字段，例如 `root`、`dest`。
2. 将关键路径转换为绝对路径。
3. 清理路径中的多余分隔符和相对片段。
4. 统一权限字符串和策略字段的内部表示。

要求：

1. 规范化后的对象应尽量不再保留未经展开的路径文本。
2. 规范化不能改变字段原本的语义，只能将其转换成更便于执行的格式。

建议实现上引入独立的中间结构，例如 `NormalizedConfig`，避免在原始结构上原地修改。

### Host 选择规则

host 选择规则在本项目中应采用严格匹配策略。

规则如下：

1. 如果命令行显式指定 `--host`，则使用该值。
2. 如果未显式指定 `--host`，则读取当前系统 `hostname`。
3. 如果最终选定的 host 名称在 `hosts` 中不存在，则直接报错。
4. 不允许在 host 未匹配时退回到 `hosts.default` 作为候选 host。

这样设计的原因是：

1. 用户必须明确知道当前运行的是哪台主机的配置。
2. 避免因隐式回退而误部署本不应启用的 profile。
3. 让报错行为尽可能早、尽可能明确。

### `hosts.default` 的语义

虽然 host 选择采用严格匹配，但 `hosts.default` 仍然保留，并承担“基础 host 配置层”的职责。

语义定义如下：

1. `hosts.default` 不是 fallback host。
2. `hosts.default` 不会在 host 未命中时被单独选中执行。
3. `hosts.default` 只在某个具体 host 已经选定后，作为其继承基础参与合并。

因此，解析流程中应先确认目标 host，再做如下处理：

1. 读取 `hosts.default`。
2. 读取 `hosts.<selected>`。
3. 以 `hosts.default` 为基础，叠加 `hosts.<selected>`。

### Host 合并规则

合并 `hosts.default` 与目标 host 时，建议采用以下规则：

1. `enable` 使用并集合并。
2. `host_profiles` 使用键合并，若发生重名应报错。
3. `overrides` 使用键合并，目标 host 中的同名项覆盖默认 host 中的同名项。

更具体地说：

1. 最终启用列表 = `hosts.default.enable` 与 `hosts.<selected>.enable` 的并集。
2. `hosts.default.host_profiles` 与 `hosts.<selected>.host_profiles` 合并后形成该主机可见的私有 profile 集合。
3. `hosts.default.overrides` 与 `hosts.<selected>.overrides` 合并后形成该主机的最终覆盖规则集合。

说明：

1. 如果未来不希望 `default` 提供 `host_profiles`，也可以在实现阶段收紧规则；但当前文档允许该能力。
2. 不论是否允许 `default.host_profiles`，最终结果都必须满足 profile 名称唯一。

### 启用集合解析

完成 host 合并后，程序需要解析最终启用的 profile 名称集合。

可见的 profile 来源有两类：

1. 全局 `profiles`
2. 当前主机可见的 `host_profiles`

解析规则：

1. 遍历最终启用列表中的每个名称。
2. 如果该名称存在于全局 `profiles`，则解析为全局 profile。
3. 如果不存在于全局 `profiles`，但存在于当前主机可见的 `host_profiles`，则解析为主机私有 profile。
4. 如果两者都不存在，则报错。

该阶段的结果应是一组“待解析 profile 引用”，供下一阶段生成最终有效配置。

### EffectiveProfile 生成

`EffectiveProfile` 是配置解析阶段最重要的产物。后续执行计划阶段不应再依赖原始 YAML 结构，而应只依赖 `EffectiveProfile`。

建议每个 `EffectiveProfile` 至少包含以下信息：

1. `name`
2. `origin_type`，用于标识它来自全局 `profiles` 还是 `host_profiles`
3. `group_name`
4. `source_root`
5. `source_path`
6. `target_root`
7. `target_path`
8. `strategy`
9. `permissions`
10. `requires_privilege`

生成顺序建议固定为：

1. 读取该 profile 引用的 `group` 默认值。
2. 叠加 `profile` 或 `host_profile` 自身定义。
3. 若存在同名 `override`，继续叠加 override。
4. 计算源路径与目标路径。
5. 判断该 profile 对应的执行是否可能需要更高权限。

这种分层可以让后续 planner 和 executor 使用统一输入，而不需要重复理解配置继承关系。

### 源路径与目标路径解析

在 `EffectiveProfile` 生成阶段，需要把逻辑字段转换成最终路径。

规则如下：

1. 源路径 = `root + group.src + profile.path`
2. 目标基础路径优先级为：`override.dest` > `profile.dest` > `group.dest`
3. 最终目标路径 = `effective_dest + basename(profile.path)`

说明：

1. 当前设计中，`profile.path` 应为相对路径。
2. 当前设计默认 profile 对应目录；后续如支持单文件 profile，再扩展路径规则。

### 预检元信息生成

配置解析阶段完成后，还需要输出一部分供执行计划阶段使用的元信息。

这些信息本身不是文件变更操作，但会影响 planner 的行为。

建议至少生成：

1. 哪些 profile 使用 `symlink`
2. 哪些 profile 使用 `recursive_symlink`
3. 哪些 profile 使用 `copy`
4. 哪些 profile 的目标位置可能涉及系统目录
5. 哪些 profile 可能需要更高权限

注意：

1. 这一阶段只做“静态预判”，不真正执行文件系统 diff。
2. 真正的目标扫描、覆盖判断、diff 生成和用户确认属于执行计划阶段。

### `copy` 策略对解析阶段的要求

本项目中，`copy` 的定义不是“替换整个目标目录”，而是“合并复制”。

其语义已经提前约定为：

1. 递归遍历源目录。
2. 如果目标目录中不存在对应子目录，则创建。
3. 如果目标目录中不存在对应文件，则复制。
4. 如果目标目录中存在同相对路径文件，则视为覆盖候选。
5. 不删除目标目录中多余的文件或目录。
6. 对覆盖候选应在执行前展示 diff。
7. 等待用户确认后再真正写入。

这一定义对解析阶段的影响是：

1. `EffectiveProfile` 必须明确携带 `copy` 策略及其权限配置。
2. 解析阶段需要为 planner 提供足够清晰的源路径、目标路径和权限信息。
3. 解析阶段不直接计算文件级 diff，但必须保证 planner 能够从解析结果中推导出 merge plan。

### 提权边界与职责划分

本项目要求在需要更高权限时，由工具自身提供交互式提权体验，而不是要求用户手动书写 `sudo <command>`。

因此，需要明确解析阶段与执行阶段的职责边界：

1. 解析阶段负责标记哪些 `EffectiveProfile` 可能需要提权。
2. 执行计划阶段负责结合实际目标路径和操作类型，判断是否真的需要提权。
3. 执行阶段负责触发交互式授权，并在授权成功后完成文件操作。

约束：

1. 程序不应通过拼接 `sudo cp`、`sudo chown` 等外部命令来完成核心文件操作。
2. 实际文件操作应由 Go 程序自身完成。
3. 提权的具体实现需要考虑 `Linux` 与 `darwin` 的平台差异，但不应改变解析阶段的数据结构设计。

说明：

1. 解析阶段只需要产出 `requires_privilege` 一类的预判信息。
2. 是否真正发起认证请求，不应在配置解析模块中决定。

### 错误分类建议

为了让用户更容易理解错误原因，建议配置加载与解析阶段输出分层错误。

推荐至少区分以下几类：

1. 配置文件定位错误
   例如默认路径下未找到配置文件。
2. 配置文件读取错误
   例如文件不可读、权限不足。
3. YAML 反序列化错误
   例如缩进错误、字段类型错误。
4. 配置结构错误
   例如 `strategy` 非法、引用不存在的 `group`。
5. Host 选择错误
   例如未指定 `--host` 且当前 `hostname` 未在 `hosts` 中定义。
6. 配置解析错误
   例如启用列表中引用了不存在的 profile。

这种错误分层可以帮助程序输出更准确的提示，也便于后续为 CLI 设计友好的报错格式。

### 建议的数据模型分层

为了让代码职责清晰，建议在实现中至少区分以下几类结构：

1. `RawConfig`
   直接对应 YAML 输入。
2. `NormalizedConfig`
   路径已展开、字段已规范化，但尚未选择 host。
3. `ResolvedHostConfig`
   已经选定 host，并完成 `hosts.default` 与具体 host 的合并。
4. `EffectiveProfile`
   已经合并完 `group`、`profile`、`override`，可直接供 planner 使用。

该分层的好处是：

1. 每一步输入输出明确。
2. 易于单元测试。
3. 易于为每个阶段提供精确报错。
4. 避免在单个结构体上堆积过多“中间态字段”。

### 推荐模块职责

虽然当前阶段不写代码，但建议在实现时按职责拆分模块：

1. `config/locator`
   负责查找配置文件路径。
2. `config/loader`
   负责读取文件并反序列化 YAML。
3. `config/validate`
   负责结构校验和字段合法性检查。
4. `config/normalize`
   负责路径展开和字段规范化。
5. `config/resolve`
   负责 host 选择、继承合并和 `EffectiveProfile` 生成。
6. `planner`
   负责基于 `EffectiveProfile` 生成具体执行计划、覆盖检查和 diff。

这样的拆分有利于保证“配置解析逻辑”和“文件系统操作逻辑”互不污染。

### 本章结论

当前阶段确认的配置加载与解析流程设计结论如下：

1. 默认配置文件名为 `dotfile-mgr.yaml`。
2. 默认搜索路径依次为 `~/.config/dotfile-manager/dotfile-mgr.yaml` 和 `~/profile/dotfile-mgr.yaml`。
3. 未指定配置路径时，使用第一个命中的默认配置文件。
4. 未显式指定 host 时，严格使用当前系统 `hostname`。
5. 若目标 host 未在 `hosts` 中定义，则直接报错。
6. `hosts.default` 保留，但仅作为具体 host 的基础配置层，不作为 fallback host。
7. `hosts.default.enable` 与目标 host 的 `enable` 使用并集合并。
8. 配置解析阶段的最终核心输出是一组 `EffectiveProfile`。
9. `copy` 的执行语义为“合并复制”，不是整目录替换。
10. diff 计算、覆盖确认和真正提权属于后续 planner 与 executor 的职责，不属于纯配置解析职责。
11. 程序需要面向 `Linux` 和 `darwin` 设计一致的数据结构和流程边界。
