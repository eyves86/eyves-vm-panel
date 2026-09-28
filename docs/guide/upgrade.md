# 升级

EyvesCloud 的安装脚本和 CLI 都围绕 GitHub Release 产物工作。升级前建议先确认当前版本、备份配置和数据库。

## 查看版本

Web 面板侧边栏底部会显示当前版本，也可以访问：

```bash
curl http://127.0.0.1:8999/api/version
```

返回示例：

```json
{
  "success": true,
  "data": {
    "version": "1.1.29"
  }
}
```

## 使用安装脚本升级

安装脚本默认使用最新 Release；在交互终端不指定版本时，会列出项目全部版本供选择：

```bash
curl -fsSL https://codeberg.org/fenhaolost/eyves-vm-panel/raw/branch/main/install.sh | sudo EYVESCLOUD_REPO=codeberg:fenhaolost/eyves-vm-panel sh
```

指定版本：

```bash
curl -fsSL https://codeberg.org/fenhaolost/eyves-vm-panel/raw/branch/main/install.sh | sudo EYVESCLOUD_REPO=codeberg:fenhaolost/eyves-vm-panel EYVESCLOUD_VERSION=v1.1.32 sh
```

交互式选择详情见「安装 → 列出所有版本并交互式选择」。

## 面板自动检测版本更新

面板（系统设置 / 侧边栏版本区）会调用 `GET /api/v1/check-update` 后台检测 GitHub 最新版本（10 分钟缓存，仅检测、不自动升级），当发现比当前更新的版本时，会在侧边栏版本号旁显示 **「有更新」** 高亮徽标，点击可跳转到对应 Release 页面。

正式级升级仍推荐通过安装脚本或 `eyvescloud cli` 的「检查并升级」完成（会更换二进制并保留配置、容器数据）。

## 被控节点升级

被控节点（Agent 模式）也可以直接使用安装脚本升级。升级后 `eyvescloud agent` 会在下一次心跳时上报新版本号，主控「节点管理」页面会同步显示。

## 升级前检查

- 确认 `/root/.eyvescloud/` 或实际配置目录已备份。
- 确认系统服务没有正在执行关键任务。
- 如果正在下载镜像或恢复快照，建议等待任务完成后再升级。
- 升级后检查 `systemctl status eyvescloud` 和 Web 面板版本号。
