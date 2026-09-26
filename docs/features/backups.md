# 实例级备份

实例级备份用于把 LXC / KVM 容器的完整磁盘数据打包归档，便于灾难恢复、重装前数据保全或跨节点搬运数据。与「快照」仅保存在快照存储不同，备份会以 tar 归档文件形式写入独立的备份目录，可跨实例删除/重装持久化。

## 备份存储位置

备份归档存放在 `{DataDir}/instance-backups` 目录，每个容器一个子目录，归档文件名为 `bak-{容器ID}-{时间戳}.tar`。创建备份时会先走一致性快照（冷拷贝）保证一致性，再归档打包，完成后自动回收临时快照。

## 创建与列出备份

```http
GET  /api/v1/containers/{id}/backups
POST /api/v1/containers/{id}/backups
```

`GET` 返回该容器的全部备份记录。创建备份时可携带 `keep` 参数：

| 字段 | 说明 |
| --- | --- |
| `keep` | 保留数量。`keep > 0` 时仅保留最新 `keep` 份，更早的备份自动轮换清理；`keep <= 0`（默认）表示全部保留、不自动清理。 |

## 还原备份

```http
POST /api/v1/containers/{id}/backups/{backupId}/restore
```

还原会停机，把备份归档解压到受保护的快照基址并复用快照还原的「停机 → 交换 → 重启」逻辑，最后重启容器。还原会改变容器状态，生产环境请先确认业务可以中断。

## 删除备份

```http
DELETE /api/v1/containers/{id}/backups/{backupId}
```

删除会同时移除归档文件与备份记录。

## 权限

- 列表：`snapshot:read`；创建：`snapshot:create`；还原：`snapshot:restore`；删除：`snapshot:delete`。
- 子用户不能创建实例备份（返回 `403`）。

## 备份记录字段

| 字段 | 说明 |
| --- | --- |
| `id` | 备份 ID（`bak-...`） |
| `container_id` / `container_name` | 所属容器 |
| `kind` | `lxc` 或 `kvm` |
| `created_at` / `created_by` | 创建时间与操作者 |
| `path` | 归档文件路径 |
| `size_bytes` | 归档大小（字节） |