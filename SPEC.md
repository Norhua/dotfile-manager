# dotfile-manager v1 目标与约束

## 文档目的

本文档用于收敛第一版实现范围，只保留当前已经确认的目标、运行流程和约束条件，作为后续编码的直接依据。

## 核心目标

1. 从配置文件中解析出三类操作：`symlink`、`recursive_symlink`、`copy`。
2. 在执行前扫描目标状态，剔除无变化项，只保留真正需要执行的操作。
3. 输出清晰简洁的文件变动日志，并在执行前要求用户确认。
4. 在需要访问受保护路径时，由工具自身发起交互式提权。
5. 第一版支持 `Linux` 和 `darwin`。

## 配置规则

1. 默认配置文件名为 `dotfile-mgr.yaml`。
2. 默认配置文件搜索顺序为：
   `~/.config/dotfile-manager/dotfile-mgr.yaml`
   `~/dotfile/dotfile-mgr.yaml`
3. 可通过命令行显式指定配置文件路径。
4. 可通过命令行显式指定 `host`。
5. 如果未显式指定 `host`，则使用当前系统 `hostname`。
6. 如果目标 `host` 不存在，则直接报错。
7. `hosts.default` 保留，但只作为被选中 `host` 的继承基础，不作为回退 host。
8. `hosts.default.enable` 与目标 `host` 的 `enable` 使用并集合并。
9. `hosts.default` 中只允许出现 `enable`，不允许出现 `host_profiles` 和 `overrides`。

## 状态文件

为了支持 profile 禁用、策略切换、旧目标清理以及受管 `copy` 文件的修改检测，第一版必须保存“上一次成功应用后的受管状态”。

状态目录规则如下：

1. 如果设置了 `DOTFILE_MANAGER_STATE_DIR`，则使用该目录。
2. 否则如果设置了 `XDG_STATE_HOME`，则使用 `$XDG_STATE_HOME/dotfile-manager`。
3. 否则回退到 `~/.local/state/dotfile-manager`。

状态文件命名规则如下：

1. 先按配置文件绝对路径计算 `config-hash`。
2. 状态文件路径为 `<state-dir>/<config-hash>/<host>.json`。

状态文件要求：

1. 文件权限为 `0600`。
2. 使用临时文件加 `rename` 的方式原子更新。
3. 只有在整次执行成功后才更新状态文件。
4. 即使通过提权执行，状态文件也必须写回原始用户的状态目录，而不是 root 的 home 目录。

状态文件至少需要记录：

1. 当前配置文件路径
2. 当前 host
3. 所有受管目标路径
4. 每个目标路径的类型，例如文件、目录、符号链接
5. 每个目标路径所属 profile
6. 每个目标路径的必要校验信息，例如链接目标、文件内容 hash、权限、属主

状态文件中的权限字段应使用更易读的八进制字符串表示，例如 `0600`、`0644`、`0755`。

对于受管 `copy` 文件，`ContentHash` 的语义为：

1. 它记录的是“上一次成功 apply 后，目标文件应具有的内容 hash”。
2. 当一次 `copy` 成功完成后，新的 `ContentHash` 可以直接使用本次源文件内容的 hash 写入状态文件，而不要求再次读取目标文件。
3. 下一次运行时，再将“当前目标文件的 hash”与状态文件中的 `ContentHash` 比较，以判断目标文件是否在上次成功 apply 后被手动修改。

## 配置文件说明

第一版配置文件只支持以下顶层键：`version`、`root`、`groups`、`profiles`、`hosts`。

配置文件示例：

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

  xxx_conf:
    group: "config_home"
    path: "xxx.conf"

  arch_flag_conf:
    group: "config_home"
    path: "arch_flage_conf"
    strategy: "recursive_symlink"
    contents_only: true

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
      - "private_app"
    host_profiles:
      private_app:
        group: "config_home"
        path: "private_app"
        strategy: "recursive_symlink"
        symlink_force: true
    overrides:
      nvim:
        strategy: "copy"
```

### 顶层键

1. `version`
   当前固定为 `1`。
2. `root`
   dotfile 仓库根目录，支持环境变量，例如 `$HOME/dotfile`。
3. `groups`
   定义分类目录到目标基础目录的映射规则。
4. `profiles`
   定义全局可启用的配置项。
5. `hosts`
   定义不同主机启用哪些配置，以及主机专属配置和覆盖项。

### `groups` 支持的键

1. `src`
   字符串，表示 `root` 下的分类目录名，例如 `config_home`、`etc`。
2. `dest`
   字符串，表示目标基础目录，支持环境变量。
3. `strategy`
   字符串，可选值只有：`symlink`、`recursive_symlink`、`copy`。
4. `symlink_force`
   可选布尔值，仅对 `symlink` 和 `recursive_symlink` 生效，默认值为 `false`。
   为 `true` 时，如果目标位置已存在需要被软链接替换的文件或目录，则直接替换；为 `false` 时直接报错。
5. `permissions`
   可选，仅对 `copy` 生效。

### `profiles` 和 `host_profiles` 支持的键

1. `group`
   字符串，必须引用已存在的 `groups.<name>`。
2. `path`
   字符串，表示分类目录下的文件或目录相对路径，例如 `nvim`、`dae`、`xxx.conf`。
3. `dest`
   可选字符串，用于覆盖 `group.dest`。
4. `strategy`
   可选字符串，用于覆盖 `group.strategy`，可选值只有：`symlink`、`recursive_symlink`、`copy`。
5. `contents_only`
   可选布尔值，默认值为 `false`。
   为 `false` 时保留 `path` 的相对路径结构；为 `true` 时仅映射目录内容本身，不保留最外层目录名。
6. `symlink_force`
   可选布尔值，用于覆盖上层 `symlink_force`。
7. `permissions`
   可选，仅对 `copy` 生效，用于覆盖上层权限配置。

### `hosts` 支持的键

1. `enable`
   字符串列表，列出当前 host 启用的 profile 名称。
2. `host_profiles`
   可选映射，定义只在当前 host 上使用的 profile。
3. `overrides`
   可选映射，用于覆盖已存在 profile 的 `dest`、`strategy`、`contents_only`、`symlink_force`、`permissions`。
4. `hosts.default` 只允许使用 `enable`。

### `permissions` 支持的键

1. `owner`
   字符串，表示复制后文件属主，例如 `root`。
2. `file_mode`
   字符串，表示文件权限，例如 `0600`、`0644`。
3. `dir_mode`
   字符串，表示目录权限，例如 `0755`。

### 路径规则

1. `groups.src` 必须是相对路径。
2. `profiles.path` 和 `host_profiles.path` 必须是相对路径。
3. `dest` 应为目标基础目录路径，可以包含环境变量。
4. 第一版一个 profile 可以对应一个文件或一个目录。
5. 第一版不支持源目录中的符号链接。
6. `contents_only` 默认值为 `false`。
7. `path` 可以包含多层相对路径，例如 `foo/bar.conf`、`apps/flags`。
8. 如果 `contents_only=false`，则目标路径为 `dest + path`，保留 `path` 的相对路径结构。
9. 如果 `contents_only=true`，则 `path` 必须指向目录，并将目录内内容按相对路径映射到 `dest` 下。
10. 如果目标路径的中间父目录不存在，工具应自动创建缺失的父目录。

## 三种操作的语义

1. `symlink`
   将 profile 对应的文件或目录作为一个符号链接映射到目标位置。
   该策略只支持 `contents_only=false`。
   第一版统一创建绝对路径符号链接。
   如果目标位置已存在，则由 `symlink_force` 决定是替换还是报错。
2. `recursive_symlink`
   在目标位置创建目录结构，对叶子文件逐个创建符号链接。
   目标目录中额外存在的文件不会被删除。
   `contents_only=false` 时，目标根目录为 `dest/path`。
   `contents_only=true` 时，目标根目录为 `dest`，目录内内容直接释放到目标目录。
   该策略只支持目录源，不支持单文件源。
   如果需要创建符号链接的位置已存在普通文件、目录或错误链接，则由 `symlink_force` 决定是替换还是报错。
3. `copy`
   `copy` 的判断顺序如下。
   第一，如果目标不存在，则直接复制。
   第二，如果目标存在但状态文件中没有该路径的受管记录，则直接报错，视为“非受管已有文件或目录”。
   第三，如果目标存在且状态文件中存在该路径的受管记录，则先比较“当前目标文件的内容 hash、属主、权限”和“状态文件中的记录”。
   如果这一步不一致，则说明目标文件在上次成功 apply 后被手动修改，直接报错。
   如果这一步一致，再比较“当前源文件”和“当前目标文件”。
   如果源和目标一致，则跳过；如果不一致，则允许覆盖，并在预览中显示 diff 或摘要。
   对目录采用合并复制语义。
   `contents_only=false` 时，目录内容合并到 `dest/path`。
   `contents_only=true` 时，目录内容直接合并到 `dest`。
   缺失目录会创建，缺失文件会复制。
   对单文件则只支持 `contents_only=false`，并直接复制到 `dest/path`。
   如果源和目标在同一路径上的类型冲突，例如目录对应文件、文件对应目录，则直接报错。
4. `permissions` 仅对 `copy` 生效。
5. `symlink_force` 仅对 `symlink` 和 `recursive_symlink` 生效，默认值为 `false`。
6. 当 `symlink_force=true` 且目标为非空目录时，允许递归删除原目录后再创建符号链接。

## 路径冲突规则

1. 在生成执行计划前，程序必须先把所有启用 profile 展开为明确的目标路径集合。
2. 如果两个已启用 profile 生成了相同的目标路径，则直接报错，不进入执行阶段。
3. 如果两个已启用 profile 的目标路径存在父子级重叠，例如 `a` 和 `a/b`，则直接报错，不进入执行阶段。
4. `contents_only=true` 适合用于像 `arch_flage_conf` 这种“目录只是为了组织源文件，目标上不需要保留目录名”的场景。
5. 为了最小化冲突，推荐只对少数明确需要的目录使用 `contents_only=true`，其余 profile 继续使用默认的 `false`。
6. 如果全局 `profiles` 和当前 host 的 `host_profiles` 存在同名项，则直接报错。

## 运行流程

1. 解析配置文件，获得需要创建的 `symlink`、`recursive_symlink` 和 `copy` 操作集合。
2. 读取上一次成功执行后的状态文件，并与当前配置解析结果做对比，生成“清理旧目标”和“创建/更新新目标”两类操作。
3. 扫描目标状态。
   扫描完成后，剔除已经正确存在的软链接、递归软链接结果和无变化的复制项，然后输出文件变动日志。
   对于 `copy`，只有当内容、属主、文件权限、目录权限都一致时，才从操作队列中剔除。
4. 如果需要读取受保护的受管目标文件、受管链接或受管清理目标，则在检查阶段请求权限。
5. 打印交互提示，让用户确认是否执行本次变更。
6. 如果用户确认，则开始执行。
   如果剩余操作不涉及受保护路径，则直接执行。
   如果剩余操作涉及 root 文件或 root 目录，则在任何写操作开始前先请求权限，再执行。
7. 全部执行成功后，写入新的状态文件。

## 变更预览规则

1. 预览只展示真正会发生的变更，不展示已被剔除的无变化项。
2. 对于允许覆盖的受管 `copy` 文件，文本且变更量适中的情况应展示 diff。
3. 对于二进制文件，或 diff 内容过大时，不展示完整 diff，只展示摘要。
4. 如果受管目标文件或受管清理目标不可读，则必须先请求权限后再分析是否发生变更以及是否允许继续。
5. 如果用户拒绝为分析阶段授权，则中止本次运行，不做盲覆盖或盲清理。

## 清理规则

1. 清理功能只针对“上一次成功状态文件中记录过、但本次期望状态中不再需要”的受管路径。
2. 只要 profile 从 `enable` 中移除、`dest` 改变、`path` 改变、`contents_only` 改变或策略改变，都可能触发清理。
3. 第一版不尝试根据目标目录内容猜测哪些文件属于旧配置，清理依据必须来自状态文件。
4. 如果旧目标和当前新目标需要复用同一路径，而旧目录中仍存在未记录文件，则直接中止。
5. 如果旧目录中存在未记录文件，则只删除受管项并保留目录，同时向用户输出提示。
6. 删除完受管项后，应自底向上尝试清理空目录。
7. 如果一个被状态文件记录的受管 `copy` 文件在目标目录被修改，则禁用或迁移时直接报错。
8. 如果一个符号链接已经存在且它当前解析后的目标与期望目标一致，则视为“正确存在”，应被计入状态文件。
9. 如果状态文件不存在，则认为没有旧受管目标需要清理，但仍允许把“正确存在”的符号链接计入新状态文件。

## 权限与执行规则

1. 工具自身负责交互式提权体验，用户不需要手动执行 `sudo xxx`。
2. 工具不通过拼接 `sudo cp`、`sudo chown` 等外部命令完成核心操作。
3. 文件复制、软链接创建、权限修改、属主修改都应由 Go 程序自身完成。
4. 如果分析阶段需要权限而用户拒绝，则直接中止。
5. 如果执行阶段需要权限而用户拒绝，则在任何写入开始前中止。
6. 清理旧目标时，删除受保护路径也必须遵守同样的提权规则。
7. 对于 `copy`，planner 应记录源文件内容 hash；executor 在真正写入前应再次确认源文件未在预览后发生变化。

## 第一版约束

1. 第一版不支持源目录中的符号链接，遇到符号链接直接报错。
2. 第一版采用 `fail-fast` 策略，遇到不可恢复错误立即停止。
3. 第一版不做自动回滚。
4. 第一版不删除未受管的额外文件或目录，只删除状态文件中记录过的受管路径。
5. 第一版默认采用一次总确认，而不是逐文件确认。
6. 路径大小写按配置严格区分，由用户保证配置与仓库内容的一致性。

## 实现判断标准

第一版实现完成后，应满足以下判断标准：

1. 能正确解析配置并生成三类操作。
2. 能在执行前识别并剔除无变化项。
3. 能输出可读的变动日志。
4. 能在需要时完成交互式提权。
5. 能在用户确认后稳定执行变更。
6. 在 `Linux` 和 `darwin` 上行为保持一致。
