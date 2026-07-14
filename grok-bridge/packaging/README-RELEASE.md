# Grok Bridge 桌面安装包说明

本包为**便携版**（解压即用），不是 App Store / 安装向导形态。

## 支持平台

- macOS Apple Silicon (`darwin_arm64`)
- macOS Intel (`darwin_amd64`)
- Windows 64-bit (`windows_amd64`)

## 快速启动

### macOS

```bash
tar -xzf grok-bridge_*_darwin_*.tar.gz
cd grok-bridge_*_darwin_*
./start.sh
```

打开：http://127.0.0.1:8080/admin

### Windows

1. 解压 zip
2. 双击 `start.bat`
3. 浏览器打开 http://127.0.0.1:8080/admin

## 首次配置

1. 复制/编辑 `config.yaml`（首次启动脚本会从 `config.example.yaml` 生成）
2. **务必设置强管理员密码**（环境变量 `GROK_BRIDGE_ADMIN_PASSWORD`，不要用默认值）
3. 在管理后台添加 Grok 账号（OAuth / 导入 JSON）
4. 创建客户端 API Key（`gb_...`）

## 客户端接入

Claude Code:

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_API_KEY=gb_your_key
```

Codex / OpenAI 兼容:

```bash
export OPENAI_BASE_URL=http://127.0.0.1:8080/v1
export OPENAI_API_KEY=gb_your_key
```

## 可选：Windows 服务

若已安装 [NSSM](https://nssm.cc/)：

```powershell
$env:GROK_BRIDGE_ADMIN_PASSWORD="your-strong-password"
.\install-service.ps1
```

## 说明

- 数据默认写在同目录 `data/grok-bridge.db`
- 本包不含 Docker
- 若 macOS 提示“无法验证开发者”，可在终端执行：
  `xattr -dr com.apple.quarantine ./grok-bridge*`
