# 子用户

子用户用于把指定容器授权给其他用户管理。它适合临时交付、拼车分配、教学实验或多人共用宿主机的场景。

## 创建访问链接

管理员选择容器后创建子用户链接：

```http
POST /api/v1/sub-user/create
```

返回内容中可能包含用户名、初始密码、访问码或访问链接。对外展示时必须脱敏，真实值只应发送给对应用户。

## 管理子用户

```http
GET /api/v1/sub-users
POST /api/v1/sub-users/{id}/rotate-password
PUT /api/v1/sub-users/{id}/edit          # 编辑用户名 / 邮箱 / 角色 / 密码
PUT /api/v1/sub-users/{id}/bind-containers  # 整体替换容器绑定集
DELETE /api/v1/sub-users/{id}            # 删除子用户（名下容器自动解绑归属）
GET /api/v1/sub-users/{id}/audit-logs
GET /api/v1/sub-users/{id}/login-logs
```

轮换密码会让旧凭证失效。审计日志和登录日志可用于排查误操作或异常访问。

创建时也可以不绑定任何容器（空账号），后续通过 `bind-containers` 追加绑定：

```http
POST /api/v1/sub-user/create
Content-Type: application/json

{ "username": "user01", "email": "user@example.com", "container_names": [] }
```

`bind-containers` 的请求体为 `{ "containers": ["<容器 ID / UUID / 名称>", ...] }`，语义是**整体替换**该子用户的容器集合；已属于其他子用户的容器会返回 409，请先在容器上变更属主（`PUT /api/v1/containers/{id}/owner`）。删除子用户只解除归属关系，容器本身保留。

## 权限范围

子用户只能管理被授权的容器。涉及全局配置、镜像管理、安全策略、API Key 等管理员功能不会开放给子用户。
