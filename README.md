# say

`say` 是一个用 Go 编写的 macOS 文档朗读终端程序。它读取本地 UTF-8 文本文档，按自然大段落和 TTS 单次调用上限组织内容。第一段音频就绪后立即显示并播放，后续段落按顺序在后台合成。播放过程中可以暂停，也可以按时间前后跳转。默认使用 macOS 系统 TTS，也可以选择实验性的 Microsoft Edge TTS。

```text
say  lesson.txt
TTS  macOS say (system voice) · 2 speech units

… preparing audio · 0/2 ready
… ready to play · 1/2 prepared
Space 播放/暂停 · ← 回退 5s · → 快进 5s

[1/2] ▶ 第一句会先显示在终端。
      ✓ played
[2/2] ▶ Then the system voice reads the next sentence.
      ✓ played

✓ Finished 2 speech units.
```

## 功能

- 打开任意本地 UTF-8 纯文本文档；`.txt`、`.md` 等扩展名均可。
- 默认调用 macOS `/usr/bin/say`，使用“系统设置”中选择的声音和语速；每个自然段或限长片段会独立合成为临时 AIFF 音频。
- 可通过 `--provider edge` 使用实验性的 Microsoft Edge Read Aloud 在线服务，合成 MP3 音频；默认声音为 `zh-CN-XiaoxiaoNeural`。
- 优先按空行识别自然大段落，同一段落中的多个句子会连续播放，单换行会规范为空格。
- 只有自然段超过单次调用上限时，才按完整句子贪心组合为尽可能大的片段；超长单句再从逗号、分号、冒号、空格等位置切分。
- 默认保证每次 TTS 调用不超过 500 个 Unicode 字符，可按服务限制调整。
- 第一个音频片段就绪后由 AVFoundation 立即播放；后续片段按文档顺序后台合成，无需等待整篇文档。
- 如果播放或快进追上尚未合成的内容，终端会显示 buffering，目标片段就绪后保持原来的播放或暂停状态。
- 每个自然段或限长片段都会先输出到终端，再开始播放；跨段跳转时也会先显示目标段落。
- 交互式终端中按空格播放或暂停，按左方向键回退 5 秒，按右方向键快进 5 秒；跳转可以跨越自然段边界。
- 按 `Ctrl-C` 可中断当前播放。

## 使用

要求 Go 1.26 或更高版本、macOS，以及启用 CGO 的 Go 工具链。构建时需要系统 Clang；播放使用 AVFoundation，不需要安装第三方播放器。默认 provider 还会调用 macOS 自带的 `/usr/bin/say`；Edge provider 需要访问 Microsoft 在线服务。

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
  -provider string
        TTS provider: system or edge (default "system")
  -rate int
        system speech rate in words per minute (default: system rate)
  -speed float
        Edge TTS speed multiplier, from 0.5 to 2.0 (default 1)
  -voice string
        provider voice name (default: provider voice)
```

按特定 TTS 长度限制播放：

```bash
go run ./cmd/say --max-chars 300 ./notes.md
```

指定系统声音和每分钟单词数：

```bash
go run ./cmd/say --voice Tingting --rate 210 ./notes.md
```

可用声音可以通过系统命令查看：

```bash
/usr/bin/say -v '?'
```

使用 Microsoft Edge TTS 的默认中文声音：

```bash
go run ./cmd/say --provider edge ./notes.md
```

指定 Edge voice short name 和相对语速：

```bash
go run ./cmd/say --provider edge --voice en-US-AriaNeural --speed 1.25 ./notes.md
```

`--rate` 仅适用于 `system`，`--speed` 仅适用于 `edge`；显式传入不兼容的参数会直接报错。Edge provider 使用 Microsoft Edge Read Aloud 的在线接口，而不是需要 Azure key/region 的正式 Azure Speech API。该接口属于实验性能力，可能随上游服务变化而失效；文档片段会发送给 Microsoft，请勿用于不应离开本机的敏感内容。

### 播放快捷键

在 stdin 和 stdout 都连接到交互式终端时：

| 按键 | 操作 |
| --- | --- |
| 空格 | 播放 / 暂停 |
| `←` | 回退 5 秒 |
| `→` | 快进 5 秒 |
| `Ctrl-C` | 中断并退出 |

左右键作用于整篇文档的音频时间轴：到达段落边界时会切换音频和终端中的当前文字；在暂停状态下跳转不会自动恢复播放。后台合成尚未结束时，跳转状态中的总时长会以 `+` 结尾，表示这是已就绪音频的时长，不是整篇文档的最终时长。输出被重定向，或 stdin 不是终端时，程序会自动播放，但不会启用这些快捷键。

## 文档边界

当前版本面向纯文本内容：输入必须是本地普通文件、内容非空且采用 UTF-8 编码。Markdown 文件会按文本读取，不会移除标题、链接或代码标记；PDF、Word 和网页解析尚未包含在当前版本中。

可定位音频播放当前只实现了 macOS。其他操作系统可以编译项目，但运行时会返回明确的不支持提示。临时 AIFF 或 MP3 音频存放在操作系统临时目录中，无论播放成功、失败还是被取消都会清理。每次 Edge TTS 网络调用最多等待 45 秒，并会随播放取消而终止。

## 开发验证

```bash
go test -race ./...
go vet ./...
go build ./...
```
