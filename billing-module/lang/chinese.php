<?php
/**
 * EyvesCloud WHMCS 模块 —— 简体中文
 * 键集与 lang/english.php 一一对应；缺失的键会自动回落英文。
 */

if (!defined('WHMCS')) {
    die('This file cannot be accessed directly');
}

$_LANG['eyves_module_name']   = 'EyvesCloud';

/* ---- 导航 / 页签 ---- */
$_LANG['overview']            = '概览';
$_LANG['monitor']             = '资源监控';
$_LANG['setting']             = '设置与重装';
$_LANG['network']             = '网络';
$_LANG['portmap']             = '端口映射';
$_LANG['snapshots']           = '快照';
$_LANG['backups']             = '备份';
$_LANG['securitys']           = '安全组';
$_LANG['sshkey']              = 'SSH 密钥';
$_LANG['iso']                 = 'ISO 与救援';
$_LANG['tasks']               = '任务记录';
$_LANG['drive']               = '数据盘';
$_LANG['password']            = '重置密码';
$_LANG['crons']               = '定时任务';

/* ---- 实例状态 ---- */
$_LANG['on']                  = '运行中';
$_LANG['off']                 = '已停止';
$_LANG['starting']            = '启动中';
$_LANG['stopping']            = '停止中';
$_LANG['creating']            = '创建中';
$_LANG['status_error']        = '异常';
$_LANG['status_unknown']      = '未知';
$_LANG['suspended']           = '已挂起';
$_LANG['locked']              = '已锁定';
$_LANG['status']              = '状态';
$_LANG['runtime']             = '虚拟化类型';

/* ---- 电源与操作 ---- */
$_LANG['boot']                = '开机';
$_LANG['shutdown']            = '关机';
$_LANG['hardshutdown']        = '强制关机';
$_LANG['reboot']              = '重启';
$_LANG['hardreboot']          = '强制重启';
$_LANG['console']             = '控制台';
$_LANG['console_ssh']         = 'SSH 终端';
$_LANG['console_vnc']         = 'VNC 控制台';
$_LANG['renewal']             = '续费';
$_LANG['submitticket']        = '提交工单';
$_LANG['refresh']             = '刷新';
$_LANG['copy']                = '复制';
$_LANG['copied']              = '已复制';
$_LANG['close']               = '关闭';
$_LANG['cancel']              = '取消';
$_LANG['confirm']             = '确认';
$_LANG['submit']              = '提交';
$_LANG['save']                = '保存';
$_LANG['delete']              = '删除';
$_LANG['create']              = '创建';
$_LANG['restore']             = '还原';
$_LANG['edit']                = '修改';
$_LANG['loading']             = '加载中…';
$_LANG['action']              = '操作';
$_LANG['actionconfirmtitle']  = '操作确认';
$_LANG['warn_irreversible']   = '此操作不可撤销，是否继续？';

/* ---- 字段 ---- */
$_LANG['area']                = '区域';
$_LANG['node']                = '节点';
$_LANG['instance']            = '实例';
$_LANG['instanceid']          = '实例 ID';
$_LANG['hostname']            = '主机名';
$_LANG['operatingsystem']     = '操作系统';
$_LANG['reinstallos']         = '重装系统';
$_LANG['ipaddr']              = 'IP 地址';
$_LANG['mainip']              = '主 IP';
$_LANG['publicipv4']          = '独立 IPv4';
$_LANG['ipv6']                = 'IPv6';
$_LANG['port']                = '端口';
$_LANG['sshport']             = 'SSH 端口';
$_LANG['vncport']             = 'VNC 端口';
$_LANG['username']            = '用户名';
$_LANG['instancepass']        = '密码';
$_LANG['cpu']                 = 'CPU 核数';
$_LANG['memory']              = '内存';
$_LANG['disk']                = '系统盘';
$_LANG['datadisk']            = '数据盘';
$_LANG['datadisk_none']       = '未挂载';
$_LANG['bandwidth']           = '带宽';
$_LANG['bandwidth_down']      = '下行';
$_LANG['bandwidth_up']        = '上行';
$_LANG['traffic']             = '流量';
$_LANG['traffic_used']        = '已用';
$_LANG['traffic_quota']       = '套餐';
$_LANG['traffic_unlimited']   = '不限';
$_LANG['traffic_error']       = '流量已超限';
$_LANG['expires']             = '到期时间';
$_LANG['created']             = '创建时间';
$_LANG['remark']              = '备注';
$_LANG['owner']               = '所属';

/* ---- 控制台 / 重装 ---- */
$_LANG['console_not_running'] = '实例未运行，控制台不可用';
$_LANG['console_suspended']   = '实例已挂起，控制台不可用';
$_LANG['reinstall_tip']       = '重装会清空系统盘数据，请输入实例名确认。';
$_LANG['reinstall_confirm']   = '请输入实例名以确认';
$_LANG['reinstall_name_mismatch'] = '实例名不匹配';
$_LANG['select_image']        = '请选择系统镜像';
$_LANG['image_runtime_mismatch'] = '镜像与实例的虚拟化类型不匹配';
$_LANG['password_rule']       = '至少 8 位，需包含字母与数字';
$_LANG['new_password']        = '新密码';
$_LANG['reset_password_tip']  = '重置密码需要实例处于运行状态。';
$_LANG['password_reset_ok']   = '密码重置成功';

/* ---- 快照 / 备份 ---- */
$_LANG['snapshot_name']       = '快照名称';
$_LANG['snapshot_create']     = '创建快照';
$_LANG['snapshot_restore']    = '还原快照';
$_LANG['snapshot_limit']      = '快照配额';
$_LANG['no_snapshots']        = '暂无快照';
$_LANG['backup_create']       = '创建备份';
$_LANG['no_backups']          = '暂无备份';
$_LANG['size']                = '大小';
$_LANG['description']         = '描述';
$_LANG['nosshkey']            = '暂无 SSH 密钥';
$_LANG['no_data']             = '暂无数据';

/* ---- 网络 / 端口映射 ---- */
$_LANG['internal_port']       = '内部端口';
$_LANG['external_port']       = '外部端口';
$_LANG['protocol']            = '协议';
$_LANG['port_used']           = '已用';
$_LANG['port_limit']          = '配额';
$_LANG['port_add']            = '添加映射';
$_LANG['no_portmap']          = '暂无端口映射';
$_LANG['random_port']         = '随机端口';

/* ---- ISO / 救援 ---- */
$_LANG['iso_mount']           = '挂载 ISO';
$_LANG['rescue_mode'] = '救援模式';
$_LANG['rescue_enter']        = '进入救援模式';
$_LANG['rescue_exit']         = '退出救援模式';
$_LANG['rescue_enabled']      = '救援模式已开启';
$_LANG['iso_kvm_only']        = 'ISO 与救援模式仅 KVM 实例可用';

/* ---- 安全组 ---- */
$_LANG['security_group']      = '安全组';
$_LANG['group_attached']      = '已关联';
$_LANG['group_available']     = '可关联';

/* ---- 任务 ---- */
$_LANG['task_action']         = '操作';
$_LANG['task_status']         = '状态';
$_LANG['task_progress']       = '进度';
$_LANG['task_message']        = '信息';
$_LANG['task_time']           = '时间';
$_LANG['task_cancel']         = '取消任务';
$_LANG['task_pending']        = '等待中';
$_LANG['task_running']        = '执行中';
$_LANG['task_success']        = '成功';
$_LANG['task_failed']         = '失败';
$_LANG['task_cancelled']      = '已取消';

/* ---- 监控 ---- */
$_LANG['range_1h']            = '最近 1 小时';
$_LANG['range_6h']            = '最近 6 小时';
$_LANG['range_24h']           = '最近 24 小时';
$_LANG['range_7d']            = '最近 7 天';
$_LANG['monitor_cpu']         = 'CPU';
$_LANG['monitor_memory']      = '内存';
$_LANG['monitor_disk']        = '磁盘';
$_LANG['monitor_net']         = '网络';
$_LANG['monitor_no_data']     = '暂无监控数据';

/* ---- 设置 ---- */
$_LANG['rename']              = '修改主机名';
$_LANG['reset_traffic']       = '重置流量';
$_LANG['reset_traffic_ok']    = '流量已重置';
$_LANG['hostname_rule']       = '仅允许字母、数字、点、下划线和连字符';

/* ---- 提示 / 错误 ---- */
$_LANG['not_provisioned']     = '该服务尚未开通实例。';
$_LANG['panel_unreachable']   = '无法连接面板';
$_LANG['operation_failed']    = '操作失败';
$_LANG['operation_success']   = '操作成功';
$_LANG['confirm_delete']      = '确定删除该项？';
$_LANG['suspended_tip']       = '该实例已挂起，请续费后恢复使用。';
$_LANG['capability_unsupported'] = '当前虚拟化类型不支持该功能。';

/* ---- 补充：原先硬编码在模板里的文案 ---- */
$_LANG['enabled']                  = '已启用';
$_LANG['disabled']                 = '未启用';
$_LANG['mounted']                  = '已挂载';
$_LANG['crons_tip']                = 'EyvesCloud 的定时能力为「自动快照计划」，可在面板侧开启。';
$_LANG['interval']                 = '间隔';
$_LANG['every_n_hours']            = '每 %s 小时';
$_LANG['exec_time']                = '执行时刻';
$_LANG['last_run']                 = '上次执行';
$_LANG['next_run']                 = '下次执行';
$_LANG['datadisk_grow_only']       = '只支持扩容，不能缩小（当前 %s GB）';
$_LANG['rescue_pick_iso']          = '进入救援模式需要指定一个 ISO，请先在下方列表选择。';
$_LANG['password_auto']            = '留空则自动生成';
$_LANG['password_save_now']        = '请立即保存，此密码不会再次展示。';
$_LANG['hostname_hint']            = '仅小写字母、数字和连字符（RFC 1123）；修改的是系统内 hostname，需实例处于运行状态';
$_LANG['image_not_downloaded']     = '（需面板下载）';
$_LANG['image_runtime_filtered']   = '仅列出与当前实例（%s）匹配的镜像';
$_LANG['sshkey_managed_by_panel']  = 'SSH 密钥由面板统一管理；如需新增或删除，请登录面板操作。';
$_LANG['connection']               = '连接方式';
$_LANG['system_info']              = '系统信息';
