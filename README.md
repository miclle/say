# say

`say` 是一个用 Go 编写的 macOS 文档朗读终端程序。它读取本地 UTF-8 文本文档，按自然大段落和 TTS 单次调用上限组织内容，在终端显示当前段落后调用系统语音朗读。

```text
say  lesson.txt
TTS  macOS say (system voice) · 2 speech units

[1/2] ▶ 第一句会先显示在终端。
      ✓ played
[2/2] ▶ Then the system voice reads the next sentence.
      ✓ played

✓ Finished 2 speech units.
```

## 功能

- 打开任意本地 UTF-8 纯文本文档；`.txt`、`.md` 等扩展名均可。
- 默认调用 macOS `/usr/bin/say`，使用“系统设置”中选择的声音和语速。
- 优先按空行识别自然大段落，同一段落中的多个句子会连续播放，单换行会规范为空格。
- 只有自然段超过单次调用上限时，才按完整句子贪心组合为尽可能大的片段；超长单句再从逗号、分号、冒号、空格等位置切分。
- 默认保证每次 TTS 调用不超过 500 个 Unicode 字符，可按服务限制调整。
- 每个自然段或限长片段都会先输出到终端，再开始播放；播放完成后显示状态。
- 按 `Ctrl-C` 可中断当前播放。

## 使用

要求 Go 1.26 或更高版本和 macOS。

在项目目录中直接运行：

```bash
go run ./cmd/say -- ./notes.md
```

也可以安装到 `GOBIN`：

```bash
go install ./cmd/say
say ./notes.md
```

安装后的命令名与 macOS 自带的 `/usr/bin/say` 相同，但本项目始终通过绝对路径调用系统程序，不会递归调用自身。

### 参数

```text
Usage: say [flags] <document>

  -max-chars int
        maximum Unicode characters per TTS call (default 500)
  -no-color
        disable ANSI terminal colors
  -rate int
        speech rate in words per minute (default: system rate)
  -voice string
        system voice name (default: System Settings)
```

按特定 TTS 长度限制播放：

```bash
go run ./cmd/say -- --max-chars 300 ./notes.md
```

指定系统声音和每分钟单词数：

```bash
go run ./cmd/say -- --voice Tingting --rate 210 ./notes.md
```

可用声音可以通过系统命令查看：

```bash
/usr/bin/say -v '?'
```

## 文档边界

当前版本面向纯文本内容：输入必须是本地普通文件、内容非空且采用 UTF-8 编码。Markdown 文件会按文本读取，不会移除标题、链接或代码标记；PDF、Word 和网页解析尚未包含在当前版本中。

系统 TTS 适配器当前只实现了 macOS。其他操作系统可以编译项目，但运行时会返回明确的不支持提示。

## 开发验证

```bash
go test -race ./...
go vet ./...
go build ./...
```
