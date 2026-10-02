<?php
/**
 * EyvesCloud WHMCS 模块 —— English (base pack)
 *
 * 本包是所有语言的基底：任何语言包缺键都会回落到这里的英文，
 * 因此这里必须覆盖模块用到的全部键。
 */

if (!defined('WHMCS')) {
    die('This file cannot be accessed directly');
}

$_LANG['eyves_module_name']   = 'EyvesCloud';

/* ---- 导航 / 页签 ---- */
$_LANG['overview']            = 'Overview';
$_LANG['monitor']             = 'Monitoring';
$_LANG['setting']             = 'Settings';
$_LANG['network']             = 'Network';
$_LANG['portmap']             = 'Port Mapping';
$_LANG['snapshots']           = 'Snapshots';
$_LANG['backups']             = 'Backups';
$_LANG['securitys']           = 'Security Groups';
$_LANG['sshkey']              = 'SSH Keys';
$_LANG['iso']                 = 'ISO & Rescue';
$_LANG['tasks']               = 'Task History';
$_LANG['drive']               = 'Data Disk';
$_LANG['password']            = 'Reset Password';
$_LANG['crons']               = 'Scheduled Tasks';

/* ---- 实例状态 ---- */
$_LANG['on']                  = 'Running';
$_LANG['off']                 = 'Stopped';
$_LANG['starting']            = 'Starting';
$_LANG['stopping']            = 'Stopping';
$_LANG['creating']            = 'Creating';
$_LANG['status_error']        = 'Failed';
$_LANG['status_unknown']      = 'Unknown';
$_LANG['suspended']           = 'Suspended';
$_LANG['locked']              = 'Locked';
$_LANG['status']              = 'Status';
$_LANG['runtime']             = 'Virtualization';

/* ---- 电源与操作 ---- */
$_LANG['boot']                = 'Start';
$_LANG['shutdown']            = 'Shut Down';
$_LANG['hardshutdown']        = 'Force Stop';
$_LANG['reboot']              = 'Restart';
$_LANG['hardreboot']          = 'Force Restart';
$_LANG['console']             = 'Console';
$_LANG['console_ssh']         = 'SSH Terminal';
$_LANG['console_vnc']         = 'VNC Console';
$_LANG['renewal']             = 'Renew';
$_LANG['submitticket']        = 'Submit Ticket';
$_LANG['refresh']             = 'Refresh';
$_LANG['copy']                = 'Copy';
$_LANG['copied']              = 'Copied';
$_LANG['close']               = 'Close';
$_LANG['cancel']              = 'Cancel';
$_LANG['confirm']             = 'Confirm';
$_LANG['submit']              = 'Submit';
$_LANG['save']                = 'Save';
$_LANG['delete']              = 'Delete';
$_LANG['create']              = 'Create';
$_LANG['restore']             = 'Restore';
$_LANG['edit']                = 'Edit';
$_LANG['loading']             = 'Loading...';
$_LANG['action']              = 'Action';
$_LANG['actionconfirmtitle']  = 'Confirm Action';
$_LANG['warn_irreversible']   = 'This action cannot be undone. Continue?';

/* ---- 字段 ---- */
$_LANG['area']                = 'Region';
$_LANG['node']                = 'Node';
$_LANG['instance']            = 'Instance';
$_LANG['instanceid']          = 'Instance ID';
$_LANG['hostname']            = 'Hostname';
$_LANG['operatingsystem']     = 'OS';
$_LANG['reinstallos']         = 'Reinstall OS';
$_LANG['ipaddr']              = 'IP Address';
$_LANG['mainip']              = 'Primary IP';
$_LANG['publicipv4']          = 'Public IPv4';
$_LANG['ipv6']                = 'IPv6';
$_LANG['port']                = 'Port';
$_LANG['sshport']             = 'SSH Port';
$_LANG['vncport']             = 'VNC Port';
$_LANG['username']            = 'Username';
$_LANG['instancepass']        = 'Password';
$_LANG['cpu']                 = 'vCPU';
$_LANG['memory']              = 'Memory';
$_LANG['disk']                = 'System Disk';
$_LANG['datadisk']            = 'Data Disk';
$_LANG['datadisk_none']       = 'Not attached';
$_LANG['bandwidth']           = 'Bandwidth';
$_LANG['bandwidth_down']      = 'Down';
$_LANG['bandwidth_up']        = 'Up';
$_LANG['traffic']             = 'Traffic';
$_LANG['traffic_used']        = 'Used';
$_LANG['traffic_quota']       = 'Quota';
$_LANG['traffic_unlimited']   = 'Unlimited';
$_LANG['traffic_error']       = 'Traffic limit exceeded';
$_LANG['expires']             = 'Expires';
$_LANG['created']             = 'Created';
$_LANG['remark']              = 'Remark';
$_LANG['owner']               = 'Owner';

/* ---- 控制台 / 重装 ---- */
$_LANG['console_not_running'] = 'Instance is not running, console unavailable';
$_LANG['console_suspended']   = 'Instance is suspended, console unavailable';
$_LANG['reinstall_tip']       = 'Reinstalling will erase the system disk. Enter the instance name to confirm.';
$_LANG['reinstall_confirm']   = 'Type the instance name to confirm';
$_LANG['reinstall_name_mismatch'] = 'Instance name does not match';
$_LANG['select_image']        = 'Select image';
$_LANG['image_runtime_mismatch'] = 'Image does not match the instance virtualization type';
$_LANG['password_rule']       = 'At least 8 characters, must include letters and digits';
$_LANG['new_password']        = 'New Password';
$_LANG['reset_password_tip']  = 'Resetting the password requires the instance to be running.';
$_LANG['password_reset_ok']   = 'Password reset successfully';

/* ---- 快照 / 备份 ---- */
$_LANG['snapshot_name']       = 'Snapshot Name';
$_LANG['snapshot_create']     = 'Create Snapshot';
$_LANG['snapshot_restore']    = 'Restore Snapshot';
$_LANG['snapshot_limit']      = 'Snapshot Quota';
$_LANG['no_snapshots']        = 'No snapshots';
$_LANG['backup_create']       = 'Create Backup';
$_LANG['no_backups']          = 'No backups';
$_LANG['size']                = 'Size';
$_LANG['description']         = 'Description';
$_LANG['nosshkey']            = 'No SSH key';
$_LANG['no_data']             = 'No data';

/* ---- 网络 / 端口映射 ---- */
$_LANG['internal_port']       = 'Internal Port';
$_LANG['external_port']       = 'External Port';
$_LANG['protocol']            = 'Protocol';
$_LANG['port_used']           = 'Used';
$_LANG['port_limit']          = 'Quota';
$_LANG['port_add']            = 'Add Mapping';
$_LANG['no_portmap']          = 'No port mappings';
$_LANG['random_port']         = 'Random Port';

/* ---- ISO / 救援 ---- */
$_LANG['iso_mount']           = 'Mount ISO';
$_LANG['rescue_mode'] = 'Rescue Mode';
$_LANG['rescue_enter']        = 'Enter Rescue Mode';
$_LANG['rescue_exit']         = 'Exit Rescue Mode';
$_LANG['rescue_enabled']      = 'Rescue mode active';
$_LANG['iso_kvm_only']        = 'ISO and rescue mode are only available for KVM instances';

/* ---- 安全组 ---- */
$_LANG['security_group']      = 'Security Group';
$_LANG['group_attached']      = 'Attached';
$_LANG['group_available']     = 'Available';

/* ---- 任务 ---- */
$_LANG['task_action']         = 'Action';
$_LANG['task_status']         = 'Status';
$_LANG['task_progress']       = 'Progress';
$_LANG['task_message']        = 'Message';
$_LANG['task_time']           = 'Time';
$_LANG['task_cancel']         = 'Cancel Task';
$_LANG['task_pending']        = 'Pending';
$_LANG['task_running']        = 'Running';
$_LANG['task_success']        = 'Success';
$_LANG['task_failed']         = 'Failed';
$_LANG['task_cancelled']      = 'Cancelled';

/* ---- 监控 ---- */
$_LANG['range_1h']            = 'Last hour';
$_LANG['range_6h']            = 'Last 6 hours';
$_LANG['range_24h']           = 'Last 24 hours';
$_LANG['range_7d']            = 'Last 7 days';
$_LANG['monitor_cpu']         = 'CPU';
$_LANG['monitor_memory']      = 'Memory';
$_LANG['monitor_disk']        = 'Disk';
$_LANG['monitor_net']         = 'Network';
$_LANG['monitor_no_data']     = 'No monitoring data';

/* ---- 设置 ---- */
$_LANG['rename']              = 'Rename';
$_LANG['reset_traffic']       = 'Reset Traffic';
$_LANG['reset_traffic_ok']    = 'Traffic counters reset';
$_LANG['hostname_rule']       = 'Only letters, digits, dots, underscores and hyphens';

/* ---- 提示 / 错误 ---- */
$_LANG['not_provisioned']     = 'This service has not been provisioned yet.';
$_LANG['panel_unreachable']   = 'Cannot reach the panel';
$_LANG['operation_failed']    = 'Operation failed';
$_LANG['operation_success']   = 'Operation succeeded';
$_LANG['confirm_delete']      = 'Delete this item?';
$_LANG['suspended_tip']       = 'This instance is suspended. Please renew to restore service.';
$_LANG['capability_unsupported'] = 'This feature is not supported by the instance virtualization type.';

/* ---- 补充：原先硬编码在模板里的文案 ---- */
$_LANG['enabled']                  = 'Enabled';
$_LANG['disabled']                 = 'Disabled';
$_LANG['mounted']                  = 'Mounted';
$_LANG['crons_tip']                = 'EyvesCloud schedules automatic snapshots; enable it from the panel.';
$_LANG['interval']                 = 'Interval';
$_LANG['every_n_hours']            = 'Every %s hours';
$_LANG['exec_time']                = 'Run at';
$_LANG['last_run']                 = 'Last run';
$_LANG['next_run']                 = 'Next run';
$_LANG['datadisk_grow_only']       = 'Grow only, cannot shrink (currently %s GB)';
$_LANG['rescue_pick_iso']          = 'Rescue mode requires an ISO — pick one from the list below.';
$_LANG['password_auto']            = 'Leave empty to auto-generate';
$_LANG['password_save_now']        = 'Save it now — this password will not be shown again.';
$_LANG['hostname_hint']            = 'Lowercase letters, digits and hyphens only (RFC 1123). Changes the in-system hostname; the instance must be running.';
$_LANG['image_not_downloaded']     = ' (needs download)';
$_LANG['image_runtime_filtered']   = 'Only images matching this instance (%s) are listed';
$_LANG['sshkey_managed_by_panel']  = 'SSH keys are managed on the panel. Sign in to the panel to add or remove keys.';
$_LANG['connection']               = 'Connection';
$_LANG['system_info']              = 'System';
