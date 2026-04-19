我想用go语言来编写一个命令行工具，他的任务是帮我将我的配置文件从～/dotfile链接到指定目录。
下面是关于这个项目我的一些想法
    1.对于～/dotfile下不同的文件夹代表不同的映射路径,即～/dotfile/分类目录/配置文件目录
      例如：～/dotfile/config_home/nvim 对应 ～/.config/nvim
            ～/dotfile/etc/dae 对应 /etc/dae
    2.有一个配置文件用于：
        配置那些配置文件处于启用状态
        可以支持多台设备
        可以配置当配置文件夹的文件链接方式，是直接将整个目录软链接，还是分别对目录中的文件创建软链接,或者是只复制
        可以对于直接复制的操作可以配置文件权限
    3.有友好的提示
因为我是初学者所以我希望这个项目可以遵守go的相关规范
对于配置文件的结构我们可能需要仔细的设计一下以支持我们需要的特性
目前处于项目的设计阶段，我们先不编码，相关问题你可以直接与我讨论，我们目前的目标是在codex_plan.md完成一份标准详细的项目实现文档

关于配置文件我目前的构想是：
```yaml
version: 1

# 默认配置，配置项同下方hosts中的配置
# 如果hostname中没有配置就使用这个，如果被配置了则覆盖
# 一般不会在这里配置profiles项的内容
default:
  etc_link:
    src: "etc"
    dest: /etc
    strategy: copy
    permissions:
      own: root

hosts:
  <hostname>:
    dotfile_home: $HOME/dotfile
    links:
      # <linkname>:
      #   src: 字符串，值为 dotfile_home 下的文件夹名
      #   dest: 路径
      #   该分类目录下的配置文件的连接方式
      #   strategy: softlink/recursive_softlink/copy
      #   force: true/false
      #   permissions:  配置权限，应该只有copy才需要这个，我不知道软连接有没有需要设置权限的情况
      #     own:  用户名
      #     file_mode: "0644"
      #     dir_mode: "0755"
      #   profiles:
      #     <profile_dir_name>: 分类目录下的具体的配置文件目录,只有在这里声明了才被启用
      #     <profile_dir_name>: 可以配置覆盖默认方式
      #       dest:
      #       strategy:
      #       permissions:
      #         own:
      #         file_mode: "0644"
      #         dir_mode: "0755"

      config_home_link:
        src: "config_home"
        dest: $HOME/.config
        strategy: softlink
        profiles:
          <profile_dir_name>:
          <profile_dir_name>:
          nvim:
            dest: $HOME
            strategy: copy
      etc_link:
        # 这些内容已在deauft中配置就不用单独配置了
        # src: "etc"
        # dest: /etc
        # strategy: copy
        # permissions:
        #   own: root
        profiles:
          dae:
            permissions:
              file_mode: "0644"
              dir_mode: "0755"

```
目前我对于这个配置结构的想法是他的主要配置（即profiles）的层级太深了有点头轻脚重，不知道这正不正常，因为我从来没有设计过配置文件，所以期待你的建议
