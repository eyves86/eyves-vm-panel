{*
 * EYVESCLOUD WHMCS 客户区模板
 *
 * 由 eyvescloud_ClientArea() 以 templatefile = 'templates/clientarea' 渲染。
 * 所有数据通过 AJAX 请求 handlers/api.php 获取（POST，返回 {success,message,data}）。
 * 服务 ID 以 service_id 传递；快照/备份等资源 ID 使用 id，二者不冲突。
 *}
{literal}
<style>
.eyvescloud-app{font-size:14px;color:#1f2937;background:#f6f8fb;padding:14px;border-radius:8px;max-width:100%;overflow:hidden}
.eyvescloud-app *{box-sizing:border-box}
.eyvescloud-toolbar{display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:10px;margin-bottom:12px}
.eyvescloud-brand{font-size:16px;font-weight:700;color:#111827}
.eyvescloud-toolbar-actions{display:flex;flex-wrap:wrap;gap:8px}
.eyvescloud-btn{height:32px;border:1px solid #d1d5db;background:#fff;color:#374151;border-radius:4px;padding:0 12px;cursor:pointer;font-size:13px;white-space:nowrap}
.eyvescloud-btn:hover{border-color:#9ca3af}
.eyvescloud-btn[disabled]{opacity:.6;cursor:not-allowed}
.eyvescloud-btn-primary{border-color:#2563eb;background:#2563eb;color:#fff}
.eyvescloud-btn-danger{border-color:#dc2626;background:#dc2626;color:#fff}
.eyvescloud-btn-dark{border-color:#000;background:#000;color:#fff}
.eyvescloud-btn-sm{height:28px;padding:0 10px;font-size:12px}
.eyvescloud-msg{border:1px solid #bfdbfe;background:#eff6ff;color:#1d4ed8;border-radius:6px;padding:10px 12px;margin-bottom:12px;display:none;white-space:pre-wrap}
.eyvescloud-msg.error{border-color:#fecaca;background:#fef2f2;color:#b91c1c}
.eyvescloud-tabs{display:flex;flex-wrap:wrap;gap:6px;border-bottom:1px solid #e5e7eb;margin-bottom:14px}
.eyvescloud-tab{border:1px solid transparent;border-bottom:none;background:transparent;color:#6b7280;padding:8px 14px;cursor:pointer;font-size:14px;border-radius:6px 6px 0 0;margin-bottom:-1px}
.eyvescloud-tab.active{background:#fff;border-color:#e5e7eb;color:#111827;font-weight:600}
.eyvescloud-panel{display:none}
.eyvescloud-panel.active{display:block}
.eyvescloud-section{background:#fff;border:1px solid #e5e7eb;border-radius:6px;margin-top:12px;padding:14px}
.eyvescloud-title{font-weight:600;margin:18px 0 8px}
.eyvescloud-title-row{display:flex;align-items:center;justify-content:space-between;gap:12px;margin:18px 0 8px}
.eyvescloud-title-row .eyvescloud-title{margin:0}
.eyvescloud-muted{color:#6b7280}
.eyvescloud-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:12px;margin-bottom:16px}
.eyvescloud-card{border:1px solid #e5e7eb;border-radius:6px;padding:12px;background:#fff}
.eyvescloud-label{color:#6b7280;font-size:12px;margin-bottom:4px}
.eyvescloud-value{font-size:18px;font-weight:600;word-break:break-all}
.eyvescloud-form{border:1px solid #e5e7eb;border-radius:6px;background:#fff;padding:12px;margin-top:8px}
.eyvescloud-row{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:10px;align-items:end}
.eyvescloud-field label{display:block;color:#6b7280;font-size:12px;margin-bottom:4px}
.eyvescloud-input,.eyvescloud-select{width:100%;height:34px;border:1px solid #d1d5db;border-radius:4px;padding:6px 8px;background:#fff;color:#1f2937}
.eyvescloud-input:focus,.eyvescloud-select:focus{border-color:#2563eb;outline:none}
.eyvescloud-actions{display:flex;gap:8px;flex-wrap:wrap}
.eyvescloud-list{display:flex;flex-direction:column;gap:10px;margin-top:8px}
.eyvescloud-item{border:1px solid #e5e7eb;border-radius:6px;background:#fff;padding:12px}
.eyvescloud-table{width:100%;border-collapse:collapse;background:#fff}
.eyvescloud-table th,.eyvescloud-table td{border:1px solid #e5e7eb;padding:8px;text-align:left;word-break:break-word}
.eyvescloud-table th{width:16%;background:#f9fafb;color:#374151;font-weight:600}
.eyvescloud-debug{margin-top:12px;border:1px dashed #d1d5db;border-radius:6px;background:#f9fafb;padding:10px;color:#374151;white-space:pre-wrap;font-size:12px;display:none}
.eyvescloud-modal-mask{position:fixed;inset:0;background:rgba(15,23,42,.42);display:none;align-items:center;justify-content:center;z-index:9999;padding:16px}
.eyvescloud-modal{width:min(440px,100%);background:#fff;border-radius:6px;border:1px solid #e5e7eb;box-shadow:0 18px 48px rgba(15,23,42,.22);padding:16px}
.eyvescloud-modal-lg{width:min(560px,100%);max-height:calc(100vh - 48px);display:flex;flex-direction:column;overflow:hidden;padding:0}
.eyvescloud-modal-header{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:18px 22px;border-bottom:1px solid #e5e7eb}
.eyvescloud-modal-title{font-size:16px;font-weight:700;color:#111827;margin-bottom:8px}
.eyvescloud-modal-header .eyvescloud-modal-title{margin-bottom:0}
.eyvescloud-modal-close{width:28px;height:28px;border:0;background:transparent;color:#6b7280;cursor:pointer;font-size:26px;line-height:22px;padding:0}
.eyvescloud-modal-body{font-size:14px;color:#4b5563;line-height:1.6;margin-bottom:14px}
.eyvescloud-modal-lg .eyvescloud-modal-body{margin:0;padding:20px 22px 6px;overflow-y:auto}
.eyvescloud-modal-lg .eyvescloud-modal-body .eyvescloud-field{margin-bottom:14px}
.eyvescloud-modal-actions{display:flex;justify-content:flex-end;gap:10px}
.eyvescloud-modal-lg .eyvescloud-modal-actions{padding:8px 22px 20px}
.eyvescloud-modal-body .eyvescloud-field{margin-bottom:12px}
.eyvescloud-field-help{color:#6b7280;font-size:12px;line-height:1.5;margin-top:6px}
/* 实例信息 */
.eyvescloud-head{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:10px;margin-bottom:12px}
.eyvescloud-mini{background:#fff;border:1px solid #e5e7eb;border-radius:6px;padding:10px}
.eyvescloud-mini-label{font-size:12px;color:#6b7280;margin-bottom:4px}
.eyvescloud-mini-value{font-size:16px;font-weight:600;color:#111827;word-break:break-all}
.eyvescloud-section-title{display:flex;align-items:center;justify-content:space-between;gap:10px;font-size:15px;font-weight:700;margin-bottom:12px;color:#111827;flex-wrap:wrap}
.eyvescloud-refresh{display:flex;align-items:center;justify-content:flex-end;gap:8px;font-size:12px;color:#6b7280;font-weight:400;flex-wrap:wrap}
.eyvescloud-refresh label{display:inline-flex;align-items:center;gap:4px;white-space:nowrap}
.eyvescloud-refresh-select{height:28px;border:1px solid #d1d5db;border-radius:4px;background:#fff;color:#374151;padding:3px 6px;font-size:12px}
.eyvescloud-refresh-btn{height:28px;border:1px solid #2563eb;background:#2563eb;color:#fff;border-radius:4px;padding:3px 8px;font-size:12px;cursor:pointer;white-space:nowrap}
.eyvescloud-refresh-btn[disabled]{opacity:.6;cursor:not-allowed}
.eyvescloud-gauges{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:12px}
.eyvescloud-gauge{display:flex;align-items:center;gap:12px;min-height:92px}
.eyvescloud-ring{--p:0%;width:78px;height:78px;border-radius:50%;background:conic-gradient(#2f80ed var(--p),#e5e7eb 0);display:grid;place-items:center;flex:0 0 auto;position:relative}
.eyvescloud-ring:before{content:"";width:66px;height:66px;border-radius:50%;background:#fff;position:absolute}
.eyvescloud-ring span{position:relative;display:inline-flex;align-items:center;justify-content:center;max-width:62px;font-size:17px;font-weight:700;line-height:1;color:#111827;white-space:nowrap;text-align:center;background:#fff;border-radius:3px;padding:0 1px}
.eyvescloud-ring[data-tight="1"] span{font-size:15px}
.eyvescloud-ring[data-tight="2"] span{font-size:14px}
.eyvescloud-gauge-title{font-weight:700;color:#111827;margin-bottom:4px}
.eyvescloud-gauge-sub{font-size:12px;color:#6b7280;line-height:1.45}
.eyvescloud-progress{height:12px;background:#e5e7eb;border-radius:999px;overflow:hidden}
.eyvescloud-progress span{display:block;height:100%;width:0;background:linear-gradient(90deg,#2f80ed,#10b981);transition:width .25s ease}
.eyvescloud-traffic-row{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:10px;align-items:center;margin-top:8px;color:#374151}
.eyvescloud-charts{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:12px}
.eyvescloud-chart{border:1px solid #e5e7eb;border-radius:6px;padding:12px;background:#fff;min-height:190px;overflow:hidden}
.eyvescloud-chart-title{display:flex;justify-content:space-between;gap:8px;align-items:center;font-weight:700;margin-bottom:8px;color:#111827;flex-wrap:wrap}
.eyvescloud-chart-value{font-size:12px;color:#6b7280;font-weight:400;text-align:right}
.eyvescloud-chart canvas{width:100%;height:132px;display:block}
/* 防火墙 */
.eyvescloud-settings{margin-bottom:8px}
.eyvescloud-settings-head{display:flex;align-items:flex-start;justify-content:space-between;gap:16px;margin-bottom:18px}
.eyvescloud-settings-title{font-size:16px;font-weight:700;color:#111827;line-height:1.35}
.eyvescloud-settings-actions{display:flex;justify-content:flex-end;align-items:center;gap:10px;margin-top:12px}
.eyvescloud-save-hint{color:#6b7280;font-size:13px}
.eyvescloud-toggle{position:relative;display:inline-flex;width:56px;height:30px;flex:0 0 auto;cursor:pointer}
.eyvescloud-toggle input{opacity:0;width:0;height:0}
.eyvescloud-toggle-slider{position:absolute;inset:0;background:#cfd5df;border-radius:999px;transition:.25s}
.eyvescloud-toggle-slider:before{content:"";position:absolute;width:22px;height:22px;border-radius:50%;background:#fff;top:4px;left:4px;transition:.25s;box-shadow:0 1px 3px rgba(15,23,42,.18)}
.eyvescloud-toggle input:checked+.eyvescloud-toggle-slider{background:#10b981}
.eyvescloud-toggle input:checked+.eyvescloud-toggle-slider:before{transform:translateX(26px)}
.eyvescloud-toggle-sm{width:36px;height:20px}
.eyvescloud-toggle-sm .eyvescloud-toggle-slider:before{width:16px;height:16px;top:2px;left:2px}
.eyvescloud-toggle-sm input:checked+.eyvescloud-toggle-slider:before{transform:translateX(16px)}
.eyvescloud-default-row{display:flex;align-items:center;justify-content:space-between;gap:12px;border:1px solid #e5e7eb;border-radius:6px;background:#fff;padding:12px 16px}
.eyvescloud-default-title{font-size:15px;font-weight:700;color:#111827}
.eyvescloud-default-row .eyvescloud-select{width:auto;min-width:130px}
.eyvescloud-note{margin-top:14px;border:1px solid #bfdbfe;border-radius:6px;background:#eff6ff;color:#1d4ed8;padding:12px 16px;line-height:1.6}
.eyvescloud-note-title{font-weight:700;margin-bottom:2px}
.eyvescloud-rules{margin-top:8px;overflow-x:auto;background:#fff}
.eyvescloud-fw-table{width:100%;min-width:900px;border-collapse:collapse;table-layout:fixed}
.eyvescloud-fw-table th,.eyvescloud-fw-table td{padding:12px 14px;border-bottom:1px solid #e5e7eb;text-align:left;vertical-align:middle;color:#111827;font-size:14px;white-space:nowrap}
.eyvescloud-fw-table th{background:#f8fafc;color:#6b7280;font-weight:600}
.eyvescloud-fw-table td:nth-child(8){white-space:normal;word-break:break-word}
.eyvescloud-fw-table th:last-child,.eyvescloud-fw-table td:last-child{text-align:right}
.eyvescloud-badge{display:inline-flex;align-items:center;padding:2px 8px;border-radius:4px;font-size:12px;font-weight:600}
.eyvescloud-badge-in{background:#dbeafe;color:#1d4ed8}
.eyvescloud-badge-out{background:#fef3c7;color:#92400e}
.eyvescloud-badge-accept{background:#d1fae5;color:#065f46}
.eyvescloud-badge-drop{background:#fee2e2;color:#991b1b}
.eyvescloud-badge-net{background:#f3f4f6;color:#374151}
.eyvescloud-badge-mounted{background:#dcfce7;color:#15803d}
.eyvescloud-badge-free{background:#f3f4f6;color:#6b7280}
.eyvescloud-icon-btn{width:30px;height:30px;border:0;border-radius:4px;background:transparent;color:#6b7280;cursor:pointer;font-size:17px;line-height:30px;padding:0;text-align:center}
.eyvescloud-icon-btn:hover{background:#f3f4f6;color:#374151}
.eyvescloud-rule-ops{display:flex;align-items:center;gap:8px;justify-content:flex-end}
.eyvescloud-warn{border:1px solid #fecaca;background:#fef2f2;color:#b91c1c;border-radius:6px;padding:10px 12px;margin-top:12px;font-size:13px;line-height:1.6}
.eyvescloud-btn:focus-visible,.eyvescloud-tab:focus-visible,.eyvescloud-icon-btn:focus-visible,
.eyvescloud-input:focus-visible,.eyvescloud-select:focus-visible,.eyvescloud-modal-close:focus-visible{outline:2px solid #2563eb;outline-offset:1px}
/* 触控目标：30px 的图标按钮在手机上偏小，抬到 34px */
@media (max-width:640px){
    .eyvescloud-icon-btn{width:34px;height:34px;line-height:34px}
    /* 表格堆叠后需要字段名，否则只剩一串值 */
    .eyvescloud-table th{background:#f3f4f6;border-bottom:0}
    .eyvescloud-table tr{border:1px solid #e5e7eb;display:block;margin-bottom:10px;border-radius:6px;overflow:hidden}
    .eyvescloud-table td{border:0;border-top:1px solid #f3f4f6}

    .eyvescloud-app{padding:10px}
    .eyvescloud-charts{grid-template-columns:1fr}
    .eyvescloud-table th,.eyvescloud-table td{display:block;width:100%}
    .eyvescloud-ring{width:70px;height:70px}
    .eyvescloud-ring:before{width:60px;height:60px}
    .eyvescloud-ring span{max-width:56px;font-size:15px}
    .eyvescloud-settings-head,.eyvescloud-default-row,.eyvescloud-settings-actions{align-items:stretch;flex-direction:column}
    .eyvescloud-default-row .eyvescloud-select,.eyvescloud-settings-actions .eyvescloud-btn{width:100%}
    .eyvescloud-title-row{align-items:stretch;flex-direction:column}
    .eyvescloud-title-row .eyvescloud-btn{width:100%}
}
</style>
{/literal}

<div class="eyvescloud-app" id="eyvescloud-app"
     data-service-id="{$service_id}"
     data-endpoint="{$module_url|escape:'html'}handlers/api.php"
     data-container-name="{$container_name|escape:'html'}"
     data-kvm="{$is_kvm}"
     data-csrf="{$csrf_token|escape:'html'}">

    <div class="eyvescloud-toolbar">
        <div class="eyvescloud-brand">EYVESCLOUD 控制台</div>
        <div class="eyvescloud-toolbar-actions">
            <button class="eyvescloud-btn eyvescloud-btn-primary" type="button" data-console="webssh">WebSSH</button>
            {if $is_kvm == '1'}<button class="eyvescloud-btn" type="button" data-console="vnc">VNC 控制台</button>{/if}
            <button class="eyvescloud-btn" type="button" data-power="powerOn">开机</button>
            <button class="eyvescloud-btn" type="button" data-power="powerOff">关机</button>
            <button class="eyvescloud-btn" type="button" data-power="powerReboot">重启</button>
            <button class="eyvescloud-btn eyvescloud-btn-warn" type="button" data-power="powerHardOff" title="强制硬关机（kill -9 级别，等同于拔电源）">硬关机</button>
            {if $is_kvm == '1'}<button class="eyvescloud-btn eyvescloud-btn-warn" type="button" data-power="rescueMode" title="挂载救援 ISO 并重启到救援模式">救援模式</button>{/if}
            {if $is_kvm == '1'}<button class="eyvescloud-btn" type="button" data-power="rescueExit" title="退出救援模式，回到硬盘启动">退出救援</button>{/if}
            <button class="eyvescloud-btn" type="button" data-sync>同步状态</button>
        </div>
    </div>

    <div class="eyvescloud-msg" data-global-msg></div>

    <div class="eyvescloud-tabs">
        <button class="eyvescloud-tab active" type="button" data-tab="info">实例信息</button>
        <button class="eyvescloud-tab" type="button" data-tab="nat">NAT转发</button>
        <button class="eyvescloud-tab" type="button" data-tab="firewall">防火墙</button>
        <button class="eyvescloud-tab" type="button" data-tab="snapshot">快照</button>
        <button class="eyvescloud-tab" type="button" data-tab="backup">备份</button>
        {if $is_kvm == '1'}<button class="eyvescloud-tab" type="button" data-tab="iso">ISO挂载</button>{/if}
        <button class="eyvescloud-tab" type="button" data-tab="reinstall">重装系统</button>
    </div>

    <div class="eyvescloud-panels">

        <!-- 实例信息 -->
        <section class="eyvescloud-panel active" data-panel="info">
            <div class="eyvescloud-info" data-info-root="1">
                <div class="eyvescloud-head">
                    <div class="eyvescloud-mini">
                        <div class="eyvescloud-mini-label">实例名称</div>
                        <div class="eyvescloud-mini-value" data-info="name">{$container_name|escape:'html'}</div>
                    </div>
                    <div class="eyvescloud-mini">
                        <div class="eyvescloud-mini-label">运行状态</div>
                        <div class="eyvescloud-mini-value" data-info="status_text">-</div>
                    </div>
                    <div class="eyvescloud-mini">
                        <div class="eyvescloud-mini-label">SSH 地址</div>
                        <div class="eyvescloud-mini-value"><span data-info="server_ip">-</span>:<span data-info="ssh_port">-</span></div>
                    </div>
                    <div class="eyvescloud-mini">
                        <div class="eyvescloud-mini-label">IPv6</div>
                        <div class="eyvescloud-mini-value" data-info="ipv6">-</div>
                    </div>
                </div>

                <div class="eyvescloud-section">
                    <div class="eyvescloud-section-title">
                        <span>状态</span>
                        <span class="eyvescloud-refresh">
                            <span>更新于 <span data-info="chart_time">-</span></span>
                            <label>
                                自动刷新
                                <select class="eyvescloud-refresh-select" data-info-refresh>
                                    <option value="0" selected>不刷新</option>
                                    <option value="10000">10 秒</option>
                                    <option value="60000">1 分钟</option>
                                    <option value="300000">5 分钟</option>
                                    <option value="600000">10 分钟</option>
                                </select>
                            </label>
                            <button class="eyvescloud-refresh-btn" type="button" data-info-refresh-now>立即刷新</button>
                        </span>
                    </div>
                    <div class="eyvescloud-gauges">
                        <div class="eyvescloud-gauge">
                            <div class="eyvescloud-ring" data-gauge="cpu_percent"><span><span data-info="cpu_percent">0</span>%</span></div>
                            <div>
                                <div class="eyvescloud-gauge-title">CPU</div>
                                <div class="eyvescloud-gauge-sub" data-info="cpu_detail">-</div>
                            </div>
                        </div>
                        <div class="eyvescloud-gauge">
                            <div class="eyvescloud-ring" data-gauge="mem_percent"><span><span data-info="mem_percent">0</span>%</span></div>
                            <div>
                                <div class="eyvescloud-gauge-title">内存</div>
                                <div class="eyvescloud-gauge-sub" data-info="mem_detail">-</div>
                            </div>
                        </div>
                        <div class="eyvescloud-gauge">
                            <div class="eyvescloud-ring" data-gauge="load_percent"><span><span data-info="load_percent">0</span>%</span></div>
                            <div>
                                <div class="eyvescloud-gauge-title">负载</div>
                                <div class="eyvescloud-gauge-sub" data-info="load_detail">-</div>
                            </div>
                        </div>
                        <div class="eyvescloud-gauge">
                            <div class="eyvescloud-ring" data-gauge="disk_percent"><span><span data-info="disk_percent">0</span>%</span></div>
                            <div>
                                <div class="eyvescloud-gauge-title">磁盘</div>
                                <div class="eyvescloud-gauge-sub" data-info="disk_detail">-</div>
                            </div>
                        </div>
                    </div>

                    <div style="margin-top:14px">
                        <div class="eyvescloud-traffic-row">
                            <div>月流量</div>
                            <div><span data-info="traffic_used_text">-</span> / <span data-info="traffic_limit_text">-</span></div>
                        </div>
                        <div class="eyvescloud-progress"><span data-progress="traffic_percent"></span></div>
                        <div class="eyvescloud-traffic-row" style="font-size:12px;color:#6b7280">
                            <div>入站 <span data-info="traffic_in_text">-</span></div>
                            <div>出站 <span data-info="traffic_out_text">-</span></div>
                        </div>
                    </div>
                </div>

                <div class="eyvescloud-section">
                    <div class="eyvescloud-section-title"><span>统计信息</span></div>
                    <div class="eyvescloud-charts">
                        <div class="eyvescloud-chart">
                            <div class="eyvescloud-chart-title">CPU 使用率 <span class="eyvescloud-chart-value" data-info="cpu_detail">-</span></div>
                            <canvas data-chart="cpu_percent"></canvas>
                        </div>
                        <div class="eyvescloud-chart">
                            <div class="eyvescloud-chart-title">内存使用 <span class="eyvescloud-chart-value" data-info="mem_detail">-</span></div>
                            <canvas data-chart="mem_percent"></canvas>
                        </div>
                        <div class="eyvescloud-chart">
                            <div class="eyvescloud-chart-title">网络流量 <span class="eyvescloud-chart-value"><span data-info="net_in_rate">0 B/s</span> / <span data-info="net_out_rate">0 B/s</span></span></div>
                            <canvas data-chart="network"></canvas>
                        </div>
                        <div class="eyvescloud-chart">
                            <div class="eyvescloud-chart-title">磁盘 IO <span class="eyvescloud-chart-value"><span data-info="disk_read_rate">0 B/s</span> / <span data-info="disk_write_rate">0 B/s</span></span></div>
                            <canvas data-chart="diskio"></canvas>
                        </div>
                    </div>
                </div>

                <div class="eyvescloud-section">
                    <div class="eyvescloud-section-title"><span>实例信息</span></div>
                    <table class="eyvescloud-table">
                        <tbody>
                            <tr>
                                <th>IPv4</th><td data-info="ipv4">-</td>
                                <th>用户名</th><td>root</td>
                            </tr>
                            <tr>
                                <th>SSH 端口</th><td data-info="ssh_port">-</td>
                                <th>SSH 密码</th><td>{if $ssh_password}{$ssh_password|escape:'html'}{else}-{/if}</td>
                            </tr>
                            <tr>
                                <th>CPU</th><td data-info="vcpu">-</td>
                                <th>内存</th><td data-info="ram_mb">-</td>
                            </tr>
                            <tr>
                                <th>硬盘</th><td data-info="disk_gb">-</td>
                                <th>带宽</th><td data-info="bandwidth">-</td>
                            </tr>
                            <tr>
                                <th>到期时间</th><td colspan="3" data-info="expires_at">-</td>
                            </tr>
                        </tbody>
                    </table>
                </div>

                <pre class="eyvescloud-debug" data-info-debug></pre>
            </div>
        </section>

        <!-- NAT 转发 -->
        <section class="eyvescloud-panel" data-panel="nat">
            <div class="eyvescloud-msg" data-msg></div>

            <div class="eyvescloud-grid">
                <div class="eyvescloud-card"><div class="eyvescloud-label">实例名称</div><div class="eyvescloud-value" data-nat-container>{$container_name|escape:'html'}</div></div>
                <div class="eyvescloud-card"><div class="eyvescloud-label">公网地址</div><div class="eyvescloud-value" data-nat-host>-</div></div>
                <div class="eyvescloud-card"><div class="eyvescloud-label">SSH 端口</div><div class="eyvescloud-value" data-nat-ssh>-</div></div>
            </div>

            <div class="eyvescloud-title">添加端口映射</div>
            <div class="eyvescloud-form">
                <div class="eyvescloud-row">
                    <div class="eyvescloud-field"><label>公网端口</label><input class="eyvescloud-input" data-nat-add="host_port" type="number" min="1" max="65535" placeholder="61320"></div>
                    <div class="eyvescloud-field"><label>容器端口</label><input class="eyvescloud-input" data-nat-add="container_port" type="number" min="1" max="65535" placeholder="8080"></div>
                    <div class="eyvescloud-field"><label>协议</label><select class="eyvescloud-select" data-nat-add="protocol"><option value="tcp">TCP</option><option value="udp">UDP</option></select></div>
                    <div class="eyvescloud-field"><label>说明</label><input class="eyvescloud-input" data-nat-add="description" type="text" placeholder="HTTP"></div>
                    <div class="eyvescloud-actions">
                        <button class="eyvescloud-btn eyvescloud-btn-primary" type="button" data-nat-action="add">添加</button>
                        <button class="eyvescloud-btn" type="button" data-nat-action="random-port">获取随机端口</button>
                    </div>
                </div>
            </div>

            <div class="eyvescloud-title">现有端口映射</div>
            <div class="eyvescloud-list" data-nat-list>
                <div class="eyvescloud-form eyvescloud-muted">正在加载…</div>
            </div>
        </section>

        <!-- 防火墙 -->
        <section class="eyvescloud-panel" data-panel="firewall">
            <div class="eyvescloud-msg" data-msg></div>

            <div class="eyvescloud-section eyvescloud-settings">
                <div class="eyvescloud-settings-head">
                    <div>
                        <div class="eyvescloud-settings-title">防火墙</div>
                        <div class="eyvescloud-muted" data-fw-status>加载中...</div>
                    </div>
                    <label class="eyvescloud-toggle">
                        <input type="checkbox" data-fw-enabled>
                        <span class="eyvescloud-toggle-slider"></span>
                    </label>
                </div>
                <div class="eyvescloud-default-row">
                    <div>
                        <div class="eyvescloud-default-title">默认动作</div>
                        <div class="eyvescloud-muted">没有命中下方规则时如何处理</div>
                    </div>
                    <select class="eyvescloud-select" data-fw-default-action>
                        <option value="DROP">未匹配拒绝</option>
                        <option value="ACCEPT">未匹配放行</option>
                    </select>
                </div>
                <div class="eyvescloud-note">
                    <div class="eyvescloud-note-title">网络范围</div>
                    <div>可配置：IPv4（NAT）。IPv4 规则覆盖 IPv4 NAT 端口映射。NAT 入站端口按容器内部端口匹配，不是宿主机公网端口。</div>
                </div>
                <div class="eyvescloud-settings-actions">
                    <span class="eyvescloud-save-hint" data-fw-save-hint>当前设置已保存</span>
                    <button class="eyvescloud-btn eyvescloud-btn-primary" type="button" data-fw-save disabled>保存设置</button>
                </div>
            </div>

            <div class="eyvescloud-title-row">
                <div class="eyvescloud-title">防火墙规则</div>
                <button class="eyvescloud-btn eyvescloud-btn-dark" type="button" data-fw-action="open-add">+ 添加规则</button>
            </div>
            <div class="eyvescloud-rules" data-fw-rules>
                <div class="eyvescloud-form eyvescloud-muted">加载中...</div>
            </div>

            <pre class="eyvescloud-debug" data-fw-debug></pre>

            <!-- 规则编辑弹窗 -->
            <div class="eyvescloud-modal-mask" data-fw-rule-modal>
                <div class="eyvescloud-modal eyvescloud-modal-lg">
                    <div class="eyvescloud-modal-header">
                        <div class="eyvescloud-modal-title" data-fw-rule-title>添加规则</div>
                        <button class="eyvescloud-modal-close" type="button" data-fw-rule-close aria-label="关闭">×</button>
                    </div>
                    <div class="eyvescloud-modal-body">
                        <div class="eyvescloud-field">
                            <label>网络</label>
                            <select class="eyvescloud-select" data-fw-rule="network">
                                <option value="ipv4">IPv4（NAT）</option>
                                <option value="ipv6">IPv6</option>
                                <option value="all">全部</option>
                            </select>
                        </div>
                        <div class="eyvescloud-field">
                            <label>方向</label>
                            <select class="eyvescloud-select" data-fw-rule="direction">
                                <option value="in">入站 (Inbound)</option>
                                <option value="out">出站 (Outbound)</option>
                            </select>
                        </div>
                        <div class="eyvescloud-field">
                            <label>协议</label>
                            <select class="eyvescloud-select" data-fw-rule="protocol">
                                <option value="tcp">TCP</option>
                                <option value="udp">UDP</option>
                            </select>
                        </div>
                        <div class="eyvescloud-field">
                            <label>端口</label>
                            <input class="eyvescloud-input" data-fw-rule="port" type="text" placeholder="如: 22 或 80,443 或 8000-9000">
                            <div class="eyvescloud-field-help">NAT 入站填容器内部端口，例如公网 22023 -&gt; 容器 22，这里填 22</div>
                        </div>
                        <div class="eyvescloud-field">
                            <label>来源 IP</label>
                            <input class="eyvescloud-input" data-fw-rule="source_ip" type="text" placeholder="如: 192.168.1.0/24">
                            <div class="eyvescloud-field-help">留空为任意 IPv4，支持 CIDR: 192.168.1.0/24</div>
                        </div>
                        <div class="eyvescloud-field">
                            <label>动作</label>
                            <select class="eyvescloud-select" data-fw-rule="action">
                                <option value="DROP">拒绝 (DROP)</option>
                                <option value="ACCEPT">放行 (ACCEPT)</option>
                            </select>
                        </div>
                        <div class="eyvescloud-field">
                            <label>描述</label>
                            <input class="eyvescloud-input" data-fw-rule="description" type="text" placeholder="规则描述">
                        </div>
                    </div>
                    <div class="eyvescloud-modal-actions">
                        <button class="eyvescloud-btn" type="button" data-fw-rule-cancel>取消</button>
                        <button class="eyvescloud-btn eyvescloud-btn-dark" type="button" data-fw-rule-confirm>确定</button>
                    </div>
                </div>
            </div>
        </section>

        <!-- 快照 -->
        <section class="eyvescloud-panel" data-panel="snapshot">
            <div class="eyvescloud-msg" data-msg></div>

            <div class="eyvescloud-grid">
                <div class="eyvescloud-card"><div class="eyvescloud-label">实例名称</div><div class="eyvescloud-value">{$container_name|escape:'html'}</div></div>
                <div class="eyvescloud-card"><div class="eyvescloud-label">快照配额</div><div class="eyvescloud-value" data-snap-quota>-</div></div>
            </div>

            <div class="eyvescloud-title">创建快照</div>
            <div class="eyvescloud-form">
                <div class="eyvescloud-actions">
                    <button class="eyvescloud-btn eyvescloud-btn-primary" type="button" data-snap-create>创建快照</button>
                </div>
                <div class="eyvescloud-muted" style="margin-top:8px">快照用于保存当前磁盘状态，可用于快速还原。</div>
            </div>

            <div class="eyvescloud-title">已有快照</div>
            <div class="eyvescloud-list" data-snap-list>
                <div class="eyvescloud-form eyvescloud-muted">正在加载…</div>
            </div>
        </section>

        <!-- 备份 -->
        <section class="eyvescloud-panel" data-panel="backup">
            <div class="eyvescloud-msg" data-msg></div>

            <div class="eyvescloud-grid">
                <div class="eyvescloud-card"><div class="eyvescloud-label">实例名称</div><div class="eyvescloud-value">{$container_name|escape:'html'}</div></div>
            </div>

            <div class="eyvescloud-title">创建备份</div>
            <div class="eyvescloud-form">
                <div class="eyvescloud-actions">
                    <button class="eyvescloud-btn eyvescloud-btn-primary" type="button" data-bak-create>创建备份</button>
                </div>
                <div class="eyvescloud-muted" style="margin-top:8px">备份保存当前磁盘的完整副本，可用于灾难恢复。创建/还原可能耗时较久，请耐心等待。</div>
            </div>

            <div class="eyvescloud-title">已有备份</div>
            <div class="eyvescloud-list" data-bak-list>
                <div class="eyvescloud-form eyvescloud-muted">正在加载…</div>
            </div>
        </section>

        {if $is_kvm == '1'}
        <!-- ISO 挂载（仅 KVM） -->
        <section class="eyvescloud-panel" data-panel="iso">
            <div class="eyvescloud-msg" data-msg></div>

            <div class="eyvescloud-grid">
                <div class="eyvescloud-card"><div class="eyvescloud-label">实例名称</div><div class="eyvescloud-value">{$container_name|escape:'html'}</div></div>
            </div>

            <div class="eyvescloud-title">可用的 ISO 镜像</div>
            <div class="eyvescloud-list" data-iso-list>
                <div class="eyvescloud-form eyvescloud-muted">正在加载…</div>
            </div>

            <div class="eyvescloud-muted" style="margin-top:14px">将 ISO 挂载到本实例的光驱，可引导安装或进入救援系统。镜像需先在 ISO 目录中启用。</div>
        </section>
        {/if}

        <!-- 重装系统 -->
        <section class="eyvescloud-panel" data-panel="reinstall">
            <div class="eyvescloud-msg" data-msg></div>

            <div class="eyvescloud-grid">
                <div class="eyvescloud-card"><div class="eyvescloud-label">实例名称</div><div class="eyvescloud-value">{$container_name|escape:'html'}</div></div>
                <div class="eyvescloud-card"><div class="eyvescloud-label">当前系统模板</div><div class="eyvescloud-value" data-rei-current>-</div></div>
            </div>

            <div class="eyvescloud-title">重装系统</div>
            <div class="eyvescloud-form">
                <div class="eyvescloud-row">
                    <div class="eyvescloud-field">
                        <label>选择系统模板</label>
                        <select class="eyvescloud-select" data-rei-template>
                            <option value="">正在加载模板…</option>
                        </select>
                    </div>
                    <div class="eyvescloud-field">
                        <label>重装范围</label>
                        <select class="eyvescloud-select" data-rei-mode>
                            <option value="full">完整重装（清空系统盘）</option>
                            <option value="system">仅重装系统盘（保留数据盘）</option>
                        </select>
                    </div>
                </div>
                <div class="eyvescloud-actions" style="margin-top:10px">
                    <button class="eyvescloud-btn eyvescloud-btn-danger" type="button" data-rei-run disabled>开始重装</button>
                </div>
            </div>

            <div class="eyvescloud-warn">
                重装系统将会清空所选范围内的磁盘数据，且可能需要数分钟完成。重装期间实例可能无法连接，请确保重要数据已备份。
            </div>
        </section>

    </div>

    <!-- 通用确认弹窗 -->
    <div class="eyvescloud-modal-mask" data-modal="confirm">
        <div class="eyvescloud-modal">
            <div class="eyvescloud-modal-title" data-confirm-title>确认操作</div>
            <div class="eyvescloud-modal-body" data-confirm-body></div>
            <div class="eyvescloud-modal-actions">
                <button class="eyvescloud-btn" type="button" data-confirm-cancel>取消</button>
                <button class="eyvescloud-btn eyvescloud-btn-danger" type="button" data-confirm-ok>确认</button>
            </div>
        </div>
    </div>
</div>

{literal}
<script>
(function () {
    var app = document.getElementById('eyvescloud-app');
    if (!app || app.getAttribute('data-bound') === '1') return;
    app.setAttribute('data-bound', '1');

    var SERVICE_ID = app.getAttribute('data-service-id') || '';
    var ENDPOINT = app.getAttribute('data-endpoint') || '';
    var IS_KVM = app.getAttribute('data-kvm') === '1';
    var CSRF_TOKEN = app.getAttribute('data-csrf') || '';
    var globalMsg = app.querySelector('[data-global-msg]');

    function escapeHtml(value) {
        return String(value == null ? '' : value)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#039;');
    }

    function number(value) {
        var n = parseFloat(value);
        return isFinite(n) ? n : 0;
    }

    function setMsg(el, type, text) {
        if (!el) return;
        el.className = 'eyvescloud-msg' + (type === 'error' ? ' error' : '');
        el.style.display = 'block';
        el.textContent = text || '';
    }

    function showGlobal(type, text) {
        setMsg(globalMsg, type, text);
    }

    function setBusy(scope, value) {
        if (!scope) return;
        scope.querySelectorAll('button, input, select').forEach(function (el) {
            el.disabled = !!value;
        });
    }

    // 统一 API 调用：返回 {success, message, data}
    function api(func, payload) {
        payload = payload || {};
        var body = new URLSearchParams();
        body.set('service_id', SERVICE_ID);
        body.set('func', func);
        body.set('token', CSRF_TOKEN);
        Object.keys(payload).forEach(function (key) {
            var value = payload[key];
            if (value === undefined || value === null) return;
            body.set(key, value);
        });
        return fetch(ENDPOINT, {
            method: 'POST',
            credentials: 'same-origin',
            headers: { 'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8' },
            body: body.toString()
        }).then(function (res) {
            return res.text();
        }).then(function (text) {
            var data;
            try {
                data = JSON.parse(text);
            } catch (e) {
                data = { success: false, message: '非 JSON 响应: ' + text, data: {} };
            }
            if (!data || typeof data !== 'object') {
                data = { success: false, message: '响应格式错误', data: {} };
            }
            if (!data.data || typeof data.data !== 'object') {
                data.data = {};
            }
            return data;
        });
    }

    /* ---------------- 通用确认弹窗 ---------------- */
    var confirmMask = app.querySelector('[data-modal="confirm"]');
    var confirmTitle = app.querySelector('[data-confirm-title]');
    var confirmBody = app.querySelector('[data-confirm-body]');
    var confirmOk = app.querySelector('[data-confirm-ok]');
    var confirmCancel = app.querySelector('[data-confirm-cancel]');
    var confirmCb = null;

    function confirmDialog(title, bodyText, okText, danger, cb) {
        confirmTitle.textContent = title || '确认操作';
        confirmBody.textContent = bodyText || '';
        confirmOk.textContent = okText || '确认';
        confirmOk.className = 'eyvescloud-btn ' + (danger ? 'eyvescloud-btn-danger' : 'eyvescloud-btn-primary');
        confirmCb = cb;
        confirmMask.style.display = 'flex';
    }
    function closeConfirm() {
        confirmCb = null;
        confirmMask.style.display = 'none';
    }
    confirmCancel.addEventListener('click', closeConfirm);
    confirmOk.addEventListener('click', function () {
        var cb = confirmCb;
        closeConfirm();
        if (cb) cb();
    });
    confirmMask.addEventListener('click', function (event) {
        if (event.target === confirmMask) closeConfirm();
    });

    /* ---------------- 选项卡 ---------------- */
    var PANELS = {};
    var loaded = {};

    function activateTab(name) {
        app.querySelectorAll('.eyvescloud-tab').forEach(function (tab) {
            tab.classList.toggle('active', tab.getAttribute('data-tab') === name);
        });
        app.querySelectorAll('.eyvescloud-panel').forEach(function (panel) {
            panel.classList.toggle('active', panel.getAttribute('data-panel') === name);
        });
        if (!loaded[name] && typeof PANELS[name] === 'function') {
            loaded[name] = true;
            PANELS[name]();
        }
    }

    app.querySelectorAll('.eyvescloud-tab').forEach(function (tab) {
        tab.addEventListener('click', function () {
            activateTab(tab.getAttribute('data-tab'));
        });
    });

    /* ---------------- 实例信息 ---------------- */
    PANELS.info = function () {
        var root = app.querySelector('[data-info-root]');
        if (!root) return;
        var debugBox = root.querySelector('[data-info-debug]');
        var refreshSelect = root.querySelector('[data-info-refresh]');
        var refreshNow = root.querySelector('[data-info-refresh-now]');
        var history = { cpu_percent: [], mem_percent: [], network_in: [], network_out: [], disk_read: [], disk_write: [] };
        var maxPoints = 18;
        var refreshTimer = null;

        function push(name, value) {
            history[name].push(number(value));
            if (history[name].length > maxPoints) history[name].shift();
        }

        function seedFromHistory(points) {
            if (!points || !points.length) return false;
            history.cpu_percent = [];
            history.mem_percent = [];
            history.network_in = [];
            history.network_out = [];
            history.disk_read = [];
            history.disk_write = [];
            points.forEach(function (p) {
                history.cpu_percent.push(number(p.cpu_percent));
                history.mem_percent.push(number(p.mem_percent));
                history.network_in.push(number(p.net_in_bps));
                history.network_out.push(number(p.net_out_bps));
                history.disk_read.push(number(p.disk_read_bps));
                history.disk_write.push(number(p.disk_write_bps));
            });
            return true;
        }

        function setText(key, value) {
            if (value !== null && typeof value === 'object') return;
            root.querySelectorAll('[data-info="' + key + '"]').forEach(function (node) {
                node.textContent = (value === '' || value === null || value === undefined) ? '-' : value;
            });
        }

        function setGauge(key, value) {
            var pct = Math.max(0, Math.min(100, number(value)));
            root.querySelectorAll('[data-gauge="' + key + '"]').forEach(function (node) {
                node.style.setProperty('--p', pct + '%');
            });
        }

        function fitGaugeText() {
            root.querySelectorAll('.eyvescloud-ring').forEach(function (ring) {
                var label = ring.querySelector('span');
                if (!label) return;
                ring.removeAttribute('data-tight');
                if (label.scrollWidth > label.clientWidth) ring.setAttribute('data-tight', '1');
                if (label.scrollWidth > label.clientWidth) ring.setAttribute('data-tight', '2');
            });
        }

        function setProgress(key, value) {
            var pct = Math.max(0, Math.min(100, number(value)));
            root.querySelectorAll('[data-progress="' + key + '"]').forEach(function (node) {
                node.style.width = pct + '%';
            });
        }

        function draw(canvas, series, colors, maxValue) {
            if (!canvas || !canvas.getContext) return;
            var rect = canvas.getBoundingClientRect();
            var ratio = window.devicePixelRatio || 1;
            var width = Math.max(220, Math.floor(rect.width || canvas.clientWidth || 220));
            var height = Math.max(120, Math.floor(rect.height || canvas.clientHeight || 132));
            if (canvas.width !== width * ratio || canvas.height !== height * ratio) {
                canvas.width = width * ratio;
                canvas.height = height * ratio;
            }
            var ctx = canvas.getContext('2d');
            if (!ctx) return;
            ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
            ctx.clearRect(0, 0, width, height);
            ctx.strokeStyle = '#e5e7eb';
            ctx.lineWidth = 1;
            for (var i = 1; i < 4; i++) {
                var y = Math.round((height / 4) * i);
                ctx.beginPath();
                ctx.moveTo(0, y);
                ctx.lineTo(width, y);
                ctx.stroke();
            }
            series.forEach(function (values, idx) {
                if (!values.length) return;
                var color = colors[idx] || '#2f80ed';
                ctx.strokeStyle = color;
                ctx.lineWidth = 2;
                ctx.beginPath();
                if (values.length === 1) {
                    var singleY = height - (Math.max(0, Math.min(maxValue, number(values[0]))) / maxValue) * (height - 6) - 3;
                    ctx.moveTo(0, singleY);
                    ctx.lineTo(width, singleY);
                    ctx.stroke();
                    ctx.fillStyle = color;
                    ctx.beginPath();
                    ctx.arc(width - 8, singleY, 3, 0, Math.PI * 2);
                    ctx.fill();
                    return;
                }
                values.forEach(function (value, i) {
                    var x = (i / (values.length - 1)) * width;
                    var y = height - (Math.max(0, Math.min(maxValue, number(value))) / maxValue) * (height - 6) - 3;
                    if (i === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y);
                });
                ctx.stroke();
            });
        }

        function redraw() {
            draw(root.querySelector('[data-chart="cpu_percent"]'), [history.cpu_percent], ['#2f80ed'], 100);
            draw(root.querySelector('[data-chart="mem_percent"]'), [history.mem_percent], ['#10b981'], 100);
            var netMax = Math.max(1, Math.max.apply(null, history.network_in.concat(history.network_out, [1])));
            draw(root.querySelector('[data-chart="network"]'), [history.network_in, history.network_out], ['#2f80ed', '#f59e0b'], netMax);
            var ioMax = Math.max(1, Math.max.apply(null, history.disk_read.concat(history.disk_write, [1])));
            draw(root.querySelector('[data-chart="diskio"]'), [history.disk_read, history.disk_write], ['#10b981', '#ef4444'], ioMax);
        }

        function showError(text) {
            if (!debugBox) return;
            debugBox.style.display = 'block';
            debugBox.textContent = text || 'info load failed';
        }

        function load() {
            if (refreshNow) refreshNow.disabled = true;
            api('infoData', {}).then(function (res) {
                if (!res.success || !res.data) {
                    showError(res.message);
                    return;
                }
                var data = res.data;
                Object.keys(data).forEach(function (key) { setText(key, data[key]); });
                ['cpu_percent', 'mem_percent', 'load_percent', 'disk_percent'].forEach(function (key) {
                    setGauge(key, data[key]);
                });
                fitGaugeText();
                setProgress('traffic_percent', data.traffic_percent);
                if (seedFromHistory(data.history)) {
                    maxPoints = Math.max(maxPoints, history.cpu_percent.length + 1);
                }
                push('cpu_percent', data.cpu_percent);
                push('mem_percent', data.mem_percent);
                push('network_in', data.net_in_bps);
                push('network_out', data.net_out_bps);
                push('disk_read', data.disk_read_bps);
                push('disk_write', data.disk_write_bps);
                redraw();
            }).catch(function (error) {
                showError(error && error.message ? error.message : 'info request failed');
            }).finally(function () {
                if (refreshNow) refreshNow.disabled = false;
            });
        }

        if (refreshNow) refreshNow.addEventListener('click', load);
        if (refreshSelect) {
            refreshSelect.addEventListener('change', function () {
                if (refreshTimer) {
                    window.clearInterval(refreshTimer);
                    refreshTimer = null;
                }
                var ms = number(refreshSelect.value);
                if (ms > 0) {
                    load();
                    refreshTimer = window.setInterval(load, ms);
                }
            });
        }
        window.addEventListener('resize', function () {
            window.setTimeout(function () { fitGaugeText(); redraw(); }, 50);
        });
        load();
    };

    /* ---------------- NAT 转发 ---------------- */
    PANELS.nat = function () {
        var panel = app.querySelector('[data-panel="nat"]');
        if (!panel) return;
        var msg = panel.querySelector('[data-msg]');
        var listEl = panel.querySelector('[data-nat-list]');
        var hostEl = panel.querySelector('[data-nat-host]');
        var sshEl = panel.querySelector('[data-nat-ssh]');
        var natHost = '';

        function field(name) {
            var el = panel.querySelector('[data-nat-add="' + name + '"]');
            return el ? el.value : '';
        }

        function render(items) {
            if (!Array.isArray(items) || items.length === 0) {
                listEl.innerHTML = '<div class="eyvescloud-form eyvescloud-muted">暂无端口映射</div>';
                return;
            }
            listEl.innerHTML = items.map(function (item) {
                var protocol = (item.protocol || 'tcp').toLowerCase();
                var index = escapeHtml(String(item.index));
                var hostPort = escapeHtml(String(item.host_port == null ? '' : item.host_port));
                var containerPort = escapeHtml(String(item.container_port == null ? '' : item.container_port));
                var desc = escapeHtml(item.description || '');
                return '<div class="eyvescloud-item" data-nat-item data-index="' + index + '">' +
                    '<div class="eyvescloud-row">' +
                    '<div class="eyvescloud-field"><label>索引</label><div class="eyvescloud-value">' + index + '</div></div>' +
                    '<div class="eyvescloud-field"><label>公网访问</label><div class="eyvescloud-value">' + escapeHtml(natHost) + ':' + hostPort + '</div></div>' +
                    '<div class="eyvescloud-field"><label>公网端口</label><input class="eyvescloud-input" data-field="host_port" type="number" min="1" max="65535" value="' + hostPort + '"></div>' +
                    '<div class="eyvescloud-field"><label>容器端口</label><input class="eyvescloud-input" data-field="container_port" type="number" min="1" max="65535" value="' + containerPort + '"></div>' +
                    '<div class="eyvescloud-field"><label>协议</label><select class="eyvescloud-select" data-field="protocol">' +
                    '<option value="tcp"' + (protocol === 'tcp' ? ' selected' : '') + '>TCP</option>' +
                    '<option value="udp"' + (protocol === 'udp' ? ' selected' : '') + '>UDP</option>' +
                    '</select></div>' +
                    '<div class="eyvescloud-field"><label>说明</label><input class="eyvescloud-input" data-field="description" type="text" value="' + desc + '"></div>' +
                    '<div class="eyvescloud-actions">' +
                    '<button class="eyvescloud-btn eyvescloud-btn-sm" type="button" data-nat-action="update">保存</button>' +
                    '<button class="eyvescloud-btn eyvescloud-btn-sm eyvescloud-btn-danger" type="button" data-nat-action="delete">删除</button>' +
                    '</div></div></div>';
            }).join('');
        }

        function load(silent) {
            return api('natList', {}).then(function (res) {
                if (!res.success) {
                    setMsg(msg, 'error', res.message);
                    listEl.innerHTML = '<div class="eyvescloud-form eyvescloud-muted">加载失败</div>';
                    return;
                }
                natHost = res.data.server_ip || natHost;
                if (hostEl) hostEl.textContent = natHost || '-';
                if (sshEl) sshEl.textContent = res.data.ssh_port || '-';
                render(res.data.port_mappings || []);
                if (!silent) setMsg(msg, 'success', res.message || '获取成功');
            }).catch(function (error) {
                setMsg(msg, 'error', error && error.message ? error.message : '请求失败');
            });
        }

        function call(func, payload) {
            setBusy(panel, true);
            return api(func, payload).then(function (res) {
                if (res.success) {
                    setMsg(msg, 'success', res.message || '操作成功');
                    if (res.data.port) {
                        var hostField = panel.querySelector('[data-nat-add="host_port"]');
                        if (hostField) hostField.value = res.data.port;
                    }
                    if (Array.isArray(res.data.port_mappings)) {
                        render(res.data.port_mappings);
                    } else if (func !== 'randomPort') {
                        load(true);
                    }
                } else {
                    setMsg(msg, 'error', res.message || '操作失败');
                }
            }).catch(function (error) {
                setMsg(msg, 'error', error && error.message ? error.message : '请求失败');
            }).finally(function () {
                setBusy(panel, false);
            });
        }

        panel.addEventListener('click', function (event) {
            var button = event.target.closest('[data-nat-action]');
            if (!button) return;
            var action = button.getAttribute('data-nat-action');

            if (action === 'random-port') {
                call('randomPort', {});
                return;
            }
            if (action === 'add') {
                call('addNat', {
                    host_port: field('host_port'),
                    container_port: field('container_port'),
                    protocol: field('protocol'),
                    description: field('description')
                });
                return;
            }

            var item = button.closest('[data-nat-item]');
            if (!item) return;
            function itemField(name) {
                var el = item.querySelector('[data-field="' + name + '"]');
                return el ? el.value : '';
            }
            var payload = {
                index: item.getAttribute('data-index'),
                host_port: itemField('host_port'),
                container_port: itemField('container_port'),
                protocol: itemField('protocol'),
                description: itemField('description')
            };
            if (action === 'delete') {
                confirmDialog('确认删除', '确认删除端口映射 ' + natHost + ':' + (payload.host_port || '-') + ' -> ' + (payload.container_port || '-') + '/' + (payload.protocol || 'tcp') + ' 吗？', '删除', true, function () {
                    call('deleteNat', { index: payload.index });
                });
                return;
            }
            call('updateNat', payload);
        });

        load(false);
    };

    /* ---------------- 防火墙 ---------------- */
    PANELS.firewall = function () {
        var panel = app.querySelector('[data-panel="firewall"]');
        if (!panel) return;
        var msg = panel.querySelector('[data-msg]');
        var debugBox = panel.querySelector('[data-fw-debug]');
        var rulesEl = panel.querySelector('[data-fw-rules]');
        var enabledEl = panel.querySelector('[data-fw-enabled]');
        var defaultEl = panel.querySelector('[data-fw-default-action]');
        var statusEl = panel.querySelector('[data-fw-status]');
        var saveBtn = panel.querySelector('[data-fw-save]');
        var hintEl = panel.querySelector('[data-fw-save-hint]');
        var ruleModal = panel.querySelector('[data-fw-rule-modal]');
        var ruleTitle = panel.querySelector('[data-fw-rule-title]');
        var currentRules = [];
        var dirty = false;
        var busy = false;
        var ruleMode = 'add';
        var editingIndex = null;

        function showDebug(data) {
            if (!debugBox) return;
            debugBox.style.display = 'block';
            debugBox.textContent = JSON.stringify(data || {}, null, 2);
        }

        function updateStatusText() {
            if (enabledEl.checked) {
                statusEl.textContent = '已启用，未匹配规则的流量将被' + (defaultEl.value === 'DROP' ? '拒绝' : '放行');
            } else {
                statusEl.textContent = '未启用时不接管该容器流量';
            }
        }

        function updateSaveState() {
            if (saveBtn) saveBtn.disabled = busy || !dirty;
            if (hintEl) hintEl.textContent = dirty ? '有未保存更改，点击保存设置后生效' : '当前设置已保存';
        }

        function setPanelBusy(value) {
            busy = !!value;
            panel.querySelectorAll('button, input, select').forEach(function (el) { el.disabled = busy; });
            updateSaveState();
        }

        function markDirty(text) {
            dirty = true;
            updateStatusText();
            updateSaveState();
            setMsg(msg, 'success', text || '设置已修改，点击保存设置后生效');
        }

        function clearDirty() {
            dirty = false;
            updateSaveState();
        }

        function portDisplay(port) { return port ? port : '所有'; }
        function sourceIpDisplay(ip) { return ip ? ip : '任意'; }
        function networkLabel(net) {
            return net === 'ipv6' ? 'IPv6' : net === 'all' ? 'ALL' : 'IPv4';
        }

        function renderRules(rules) {
            currentRules = Array.isArray(rules) ? rules : [];
            if (currentRules.length === 0) {
                rulesEl.innerHTML = '<div class="eyvescloud-form eyvescloud-muted">暂无防火墙规则</div>';
                return;
            }
            var rows = currentRules.map(function (rule, idx) {
                var dir = (rule.direction || 'in').toLowerCase();
                var proto = (rule.protocol || 'tcp').toLowerCase();
                var action = ((rule.action || 'ACCEPT') + '').toUpperCase() === 'ACCEPT' ? 'ACCEPT' : 'DROP';
                var network = (rule.network || 'ipv4').toLowerCase();
                var enabled = rule.enabled !== false;
                return '<tr data-rule-index="' + idx + '">' +
                    '<td><label class="eyvescloud-toggle eyvescloud-toggle-sm"><input type="checkbox" class="eyvescloud-fw-rule-enabled" ' + (enabled ? 'checked' : '') + '><span class="eyvescloud-toggle-slider"></span></label></td>' +
                    '<td><span class="eyvescloud-badge eyvescloud-badge-net">' + networkLabel(network) + '</span></td>' +
                    '<td><span class="eyvescloud-badge ' + (dir === 'in' ? 'eyvescloud-badge-in' : 'eyvescloud-badge-out') + '">' + (dir === 'in' ? '入站' : '出站') + '</span></td>' +
                    '<td>' + escapeHtml(proto.toUpperCase()) + '</td>' +
                    '<td>' + escapeHtml(portDisplay(rule.port)) + '</td>' +
                    '<td>' + escapeHtml(sourceIpDisplay(rule.source_ip)) + '</td>' +
                    '<td><span class="eyvescloud-badge ' + (action === 'ACCEPT' ? 'eyvescloud-badge-accept' : 'eyvescloud-badge-drop') + '">' + (action === 'ACCEPT' ? '放行' : '拒绝') + '</span></td>' +
                    '<td>' + (escapeHtml(rule.description || '') || '-') + '</td>' +
                    '<td><div class="eyvescloud-rule-ops">' +
                    '<button class="eyvescloud-icon-btn" type="button" data-fw-action="edit" title="编辑" aria-label="编辑">&#9998;</button>' +
                    '<button class="eyvescloud-icon-btn" type="button" data-fw-action="delete" title="删除" aria-label="删除">&#9003;</button>' +
                    '</div></td></tr>';
            }).join('');

            rulesEl.innerHTML = '<table class="eyvescloud-fw-table">' +
                '<thead><tr>' +
                '<th style="width:88px">状态</th>' +
                '<th style="width:110px">网络</th>' +
                '<th style="width:110px">方向</th>' +
                '<th style="width:100px">协议</th>' +
                '<th style="width:110px">端口</th>' +
                '<th>来源/目标 IP</th>' +
                '<th style="width:110px">动作</th>' +
                '<th>描述</th>' +
                '<th style="width:100px;text-align:right">操作</th>' +
                '</tr></thead><tbody>' + rows + '</tbody></table>';

            rulesEl.querySelectorAll('.eyvescloud-fw-rule-enabled').forEach(function (toggle, idx) {
                toggle.addEventListener('change', function () {
                    var rule = currentRules[idx];
                    if (!rule) return;
                    rule.enabled = toggle.checked;
                    markDirty('规则开关已修改，点击保存设置后生效');
                });
            });
        }

        function ruleField(name) {
            var el = panel.querySelector('[data-fw-rule="' + name + '"]');
            return el ? el.value : '';
        }
        function setRuleField(name, value) {
            var el = panel.querySelector('[data-fw-rule="' + name + '"]');
            if (el) el.value = value == null ? '' : value;
        }
        function clearRuleForm() {
            setRuleField('network', 'ipv4');
            setRuleField('direction', 'in');
            setRuleField('protocol', 'tcp');
            setRuleField('port', '');
            setRuleField('source_ip', '');
            setRuleField('action', 'DROP');
            setRuleField('description', '');
        }
        function fillRuleForm(rule) {
            rule = rule || {};
            setRuleField('network', rule.network || 'ipv4');
            setRuleField('direction', rule.direction || 'in');
            setRuleField('protocol', rule.protocol || 'tcp');
            setRuleField('port', rule.port || '');
            setRuleField('source_ip', rule.source_ip || '');
            setRuleField('action', rule.action || 'DROP');
            setRuleField('description', rule.description || '');
        }
        function ruleFormPayload(base) {
            base = base || {};
            return {
                id: base.id || '',
                network: ruleField('network') || 'ipv4',
                direction: ruleField('direction') || 'in',
                protocol: ruleField('protocol') || 'tcp',
                port: ruleField('port'),
                source_ip: ruleField('source_ip'),
                action: ruleField('action') || 'DROP',
                description: ruleField('description'),
                enabled: base ? base.enabled !== false : true
            };
        }
        function openRuleModal(mode, idx) {
            ruleMode = mode === 'edit' ? 'edit' : 'add';
            editingIndex = ruleMode === 'edit' ? idx : null;
            if (ruleTitle) ruleTitle.textContent = ruleMode === 'edit' ? '编辑规则' : '添加规则';
            if (ruleMode === 'edit' && currentRules[idx]) fillRuleForm(currentRules[idx]);
            else clearRuleForm();
            ruleModal.style.display = 'flex';
        }
        function closeRuleModal() {
            ruleMode = 'add';
            editingIndex = null;
            ruleModal.style.display = 'none';
        }

        function saveFirewall() {
            var rules = currentRules.map(function (r) {
                return {
                    id: r.id || '',
                    network: r.network || 'ipv4',
                    direction: r.direction || 'in',
                    protocol: r.protocol || 'tcp',
                    port: r.port || '',
                    source_ip: r.source_ip || '',
                    action: r.action || 'ACCEPT',
                    description: r.description || '',
                    enabled: r.enabled !== false
                };
            });
            setPanelBusy(true);
            api('firewallUpdate', {
                enabled: enabledEl.checked ? 'true' : 'false',
                default_action: defaultEl.value,
                rules: JSON.stringify(rules)
            }).then(function (res) {
                if (!res.success) {
                    setMsg(msg, 'error', res.message || '更新失败');
                    return;
                }
                setMsg(msg, 'success', res.message || '防火墙设置已更新');
                if (res.data && Object.prototype.hasOwnProperty.call(res.data, 'enabled')) {
                    enabledEl.checked = res.data.enabled === true || res.data.enabled === 'true' || res.data.enabled === 1;
                }
                if (res.data && res.data.default_action) defaultEl.value = res.data.default_action;
                if (res.data && Array.isArray(res.data.rules)) {
                    currentRules = res.data.rules;
                    renderRules(currentRules);
                }
                clearDirty();
                updateStatusText();
            }).catch(function (error) {
                setMsg(msg, 'error', error && error.message ? error.message : '请求失败');
            }).finally(function () {
                setPanelBusy(false);
            });
        }

        function loadFirewall() {
            setPanelBusy(true);
            api('firewallList', {}).then(function (res) {
                if (!res.success) {
                    setMsg(msg, 'error', res.message || '获取防火墙设置失败');
                    rulesEl.innerHTML = '<div class="eyvescloud-form eyvescloud-muted">加载失败</div>';
                    return;
                }
                enabledEl.checked = res.data.enabled === true || res.data.enabled === 'true' || res.data.enabled === 1;
                if (res.data.default_action) defaultEl.value = res.data.default_action;
                currentRules = Array.isArray(res.data.rules) ? res.data.rules : [];
                renderRules(currentRules);
                clearDirty();
                updateStatusText();
            }).catch(function (error) {
                setMsg(msg, 'error', error && error.message ? error.message : '请求失败');
                rulesEl.innerHTML = '<div class="eyvescloud-form eyvescloud-muted">加载失败</div>';
            }).finally(function () {
                setPanelBusy(false);
            });
        }

        panel.addEventListener('click', function (event) {
            var button = event.target.closest('[data-fw-action]');
            if (!button) return;
            var action = button.getAttribute('data-fw-action');
            if (action === 'open-add') {
                openRuleModal('add');
                return;
            }
            var row = button.closest('[data-rule-index]');
            if (!row) return;
            var idx = parseInt(row.getAttribute('data-rule-index'), 10);
            if (isNaN(idx) || !currentRules[idx]) return;
            if (action === 'edit') {
                openRuleModal('edit', idx);
                return;
            }
            if (action === 'delete') {
                var rule = currentRules[idx];
                var desc = rule.description || ((rule.protocol || 'tcp') + '/' + (rule.port || 'all'));
                confirmDialog('确认删除', '确认删除规则: ' + desc + ' ?', '删除', true, function () {
                    currentRules.splice(idx, 1);
                    renderRules(currentRules);
                    markDirty('规则已删除，点击保存设置后生效');
                });
            }
        });

        panel.querySelector('[data-fw-rule-close]').addEventListener('click', closeRuleModal);
        panel.querySelector('[data-fw-rule-cancel]').addEventListener('click', closeRuleModal);
        ruleModal.addEventListener('click', function (event) {
            if (event.target === ruleModal) closeRuleModal();
        });
        panel.querySelector('[data-fw-rule-confirm]').addEventListener('click', function () {
            if (ruleMode === 'edit' && editingIndex !== null && currentRules[editingIndex]) {
                currentRules[editingIndex] = ruleFormPayload(currentRules[editingIndex]);
                renderRules(currentRules);
                closeRuleModal();
                markDirty('规则已更新，点击保存设置后生效');
                return;
            }
            currentRules.push(ruleFormPayload(null));
            renderRules(currentRules);
            closeRuleModal();
            clearRuleForm();
            markDirty('规则已添加，点击保存设置后生效');
        });

        enabledEl.addEventListener('change', function () {
            updateStatusText();
            markDirty('防火墙开关已修改，点击保存设置后生效');
        });
        defaultEl.addEventListener('change', function () {
            updateStatusText();
            markDirty('默认动作已修改，点击保存设置后生效');
        });
        saveBtn.addEventListener('click', saveFirewall);

        updateSaveState();
        loadFirewall();
    };

    /* ---------------- 快照 ---------------- */
    PANELS.snapshot = function () {
        var panel = app.querySelector('[data-panel="snapshot"]');
        if (!panel) return;
        var msg = panel.querySelector('[data-msg]');
        var listEl = panel.querySelector('[data-snap-list]');
        var quotaEl = panel.querySelector('[data-snap-quota]');

        function render(items) {
            if (!Array.isArray(items) || items.length === 0) {
                listEl.innerHTML = '<div class="eyvescloud-form eyvescloud-muted">暂无快照</div>';
                return;
            }
            listEl.innerHTML = items.map(function (item) {
                var id = escapeHtml(item.id || '');
                return '<div class="eyvescloud-item" data-snap-item data-id="' + id + '">' +
                    '<div class="eyvescloud-row">' +
                    '<div><div class="eyvescloud-label">快照 ID</div><div class="eyvescloud-value">' + id + '</div></div>' +
                    '<div><div class="eyvescloud-label">创建时间</div><div>' + escapeHtml(item.created_at || '-') + '</div></div>' +
                    '<div><div class="eyvescloud-label">大小</div><div>' + escapeHtml(item.size_text || '-') + '</div></div>' +
                    '<div><div class="eyvescloud-label">创建者</div><div>' + escapeHtml(item.created_by || '-') + '</div></div>' +
                    '<div class="eyvescloud-actions">' +
                    '<button class="eyvescloud-btn eyvescloud-btn-sm" type="button" data-snap-action="restore">还原</button>' +
                    '<button class="eyvescloud-btn eyvescloud-btn-sm eyvescloud-btn-danger" type="button" data-snap-action="delete">删除</button>' +
                    '</div></div></div>';
            }).join('');
        }

        function load(silent) {
            return api('snapshotList', {}).then(function (res) {
                if (!res.success) {
                    setMsg(msg, 'error', res.message || '获取快照列表失败');
                    listEl.innerHTML = '<div class="eyvescloud-form eyvescloud-muted">加载失败</div>';
                    return;
                }
                if (typeof res.data.quota === 'number' && quotaEl) quotaEl.textContent = res.data.quota;
                render(res.data.snapshots || []);
                if (!silent) setMsg(msg, 'success', res.message || '获取成功');
            }).catch(function (error) {
                setMsg(msg, 'error', error && error.message ? error.message : '请求失败');
            });
        }

        function call(func, payload, okText) {
            setBusy(panel, true);
            return api(func, payload).then(function (res) {
                if (res.success) {
                    setMsg(msg, 'success', res.message || okText);
                    load(true);
                } else {
                    setMsg(msg, 'error', res.message || '操作失败');
                }
            }).catch(function (error) {
                setMsg(msg, 'error', error && error.message ? error.message : '请求失败');
            }).finally(function () {
                setBusy(panel, false);
            });
        }

        panel.addEventListener('click', function (event) {
            if (event.target.closest('[data-snap-create]')) {
                call('snapshotCreate', {}, '快照已创建');
                return;
            }
            var button = event.target.closest('[data-snap-action]');
            if (!button) return;
            var item = button.closest('[data-snap-item]');
            if (!item) return;
            var id = item.getAttribute('data-id');
            if (button.getAttribute('data-snap-action') === 'restore') {
                confirmDialog('还原快照', '确认将该实例还原到该快照？当前数据将被覆盖。', '还原', true, function () {
                    call('snapshotRestore', { id: id }, '还原任务已提交');
                });
            } else {
                confirmDialog('删除快照', '确认删除该快照？删除后不可恢复。', '删除', true, function () {
                    call('snapshotDelete', { id: id }, '快照已删除');
                });
            }
        });

        load(false);
    };

    /* ---------------- 备份 ---------------- */
    PANELS.backup = function () {
        var panel = app.querySelector('[data-panel="backup"]');
        if (!panel) return;
        var msg = panel.querySelector('[data-msg]');
        var listEl = panel.querySelector('[data-bak-list]');

        function statusText(status) {
            var s = String(status || '').toLowerCase();
            if (s === 'succeeded' || s === 'completed' || s === 'done') return '已完成';
            if (s === 'running' || s === 'pending' || s === 'creating') return '进行中';
            if (s === 'failed' || s === 'error') return '失败';
            return s || '-';
        }

        function render(items) {
            if (!Array.isArray(items) || items.length === 0) {
                listEl.innerHTML = '<div class="eyvescloud-form eyvescloud-muted">暂无备份</div>';
                return;
            }
            listEl.innerHTML = items.map(function (item) {
                var id = escapeHtml(item.id || '');
                var name = escapeHtml(item.name || item.filename || item.id || '');
                return '<div class="eyvescloud-item" data-bak-item data-id="' + id + '">' +
                    '<div class="eyvescloud-row">' +
                    '<div><div class="eyvescloud-label">备份名称</div><div class="eyvescloud-value">' + name + '</div></div>' +
                    '<div><div class="eyvescloud-label">创建时间</div><div>' + escapeHtml(item.created_at || '-') + '</div></div>' +
                    '<div><div class="eyvescloud-label">大小</div><div>' + escapeHtml(item.size_text || '-') + '</div></div>' +
                    '<div><div class="eyvescloud-label">状态</div><div>' + escapeHtml(statusText(item.status)) + '</div></div>' +
                    '<div class="eyvescloud-actions">' +
                    '<button class="eyvescloud-btn eyvescloud-btn-sm" type="button" data-bak-action="restore">还原</button>' +
                    '<button class="eyvescloud-btn eyvescloud-btn-sm eyvescloud-btn-danger" type="button" data-bak-action="delete">删除</button>' +
                    '</div></div></div>';
            }).join('');
        }

        function load(silent) {
            return api('backupList', {}).then(function (res) {
                if (!res.success) {
                    setMsg(msg, 'error', res.message || '获取备份列表失败');
                    listEl.innerHTML = '<div class="eyvescloud-form eyvescloud-muted">加载失败</div>';
                    return;
                }
                render(res.data.backups || []);
                if (!silent) setMsg(msg, 'success', res.message || '获取成功');
            }).catch(function (error) {
                setMsg(msg, 'error', error && error.message ? error.message : '请求失败');
            });
        }

        function call(func, payload, okText) {
            setBusy(panel, true);
            return api(func, payload).then(function (res) {
                if (res.success) {
                    setMsg(msg, 'success', res.message || okText);
                    load(true);
                } else {
                    setMsg(msg, 'error', res.message || '操作失败');
                }
            }).catch(function (error) {
                setMsg(msg, 'error', error && error.message ? error.message : '请求失败');
            }).finally(function () {
                setBusy(panel, false);
            });
        }

        panel.addEventListener('click', function (event) {
            if (event.target.closest('[data-bak-create]')) {
                call('backupCreate', {}, '备份已创建');
                return;
            }
            var button = event.target.closest('[data-bak-action]');
            if (!button) return;
            var item = button.closest('[data-bak-item]');
            if (!item) return;
            var id = item.getAttribute('data-id');
            if (button.getAttribute('data-bak-action') === 'restore') {
                confirmDialog('还原备份', '确认将该实例还原到该备份？当前数据将被覆盖，且还原耗时较长。', '还原', true, function () {
                    call('backupRestore', { id: id }, '还原任务已提交');
                });
            } else {
                confirmDialog('删除备份', '确认删除该备份？删除后不可恢复。', '删除', true, function () {
                    call('backupDelete', { id: id }, '备份已删除');
                });
            }
        });

        load(false);
    };

    /* ---------------- ISO 挂载（仅 KVM） ---------------- */
    PANELS.iso = function () {
        var panel = app.querySelector('[data-panel="iso"]');
        if (!panel) return;
        var msg = panel.querySelector('[data-msg]');
        var listEl = panel.querySelector('[data-iso-list]');
        var containerId = 0;

        function render(items) {
            if (!Array.isArray(items) || items.length === 0) {
                listEl.innerHTML = '<div class="eyvescloud-form eyvescloud-muted">暂无可用 ISO 镜像</div>';
                return;
            }
            listEl.innerHTML = items.map(function (iso) {
                var id = escapeHtml(iso.id || '');
                var name = escapeHtml(iso.name || iso.id || '');
                var attached = parseInt(iso.attached_to_container_id || 0, 10) === containerId;
                var badge = attached
                    ? '<span class="eyvescloud-badge eyvescloud-badge-mounted">已挂载</span>'
                    : '<span class="eyvescloud-badge eyvescloud-badge-free">未挂载</span>';
                var btn = attached
                    ? '<button class="eyvescloud-btn eyvescloud-btn-sm eyvescloud-btn-danger" type="button" data-iso-action="detach" data-iso-id="' + id + '" data-iso-name="' + name + '">卸载</button>'
                    : '<button class="eyvescloud-btn eyvescloud-btn-sm" type="button" data-iso-action="attach" data-iso-id="' + id + '" data-iso-name="' + name + '">挂载</button>';
                return '<div class="eyvescloud-item" data-iso-id="' + id + '">' +
                    '<div class="eyvescloud-row">' +
                    '<div><div class="eyvescloud-label">镜像名称</div><div class="eyvescloud-value">' + name + '</div></div>' +
                    '<div><div class="eyvescloud-label">系统</div><div>' + escapeHtml(iso.os || '-') + '</div></div>' +
                    '<div><div class="eyvescloud-label">大小</div><div>' + escapeHtml(iso.size_text || '-') + '</div></div>' +
                    '<div><div class="eyvescloud-label">状态</div><div>' + badge + '</div></div>' +
                    '<div class="eyvescloud-actions">' + btn + '</div>' +
                    '</div></div>';
            }).join('');
        }

        function load(silent) {
            return api('isoList', {}).then(function (res) {
                if (!res.success) {
                    setMsg(msg, 'error', res.message || '获取 ISO 列表失败');
                    listEl.innerHTML = '<div class="eyvescloud-form eyvescloud-muted">加载失败</div>';
                    return;
                }
                containerId = parseInt(res.data.container_id || 0, 10) || 0;
                render(res.data.isos || []);
                if (!silent) setMsg(msg, 'success', res.message || '获取成功');
            }).catch(function (error) {
                setMsg(msg, 'error', error && error.message ? error.message : '请求失败');
            });
        }

        function call(func, payload, okText) {
            setBusy(panel, true);
            return api(func, payload).then(function (res) {
                if (res.success) {
                    setMsg(msg, 'success', res.message || okText);
                    load(true);
                } else {
                    setMsg(msg, 'error', res.message || '操作失败');
                }
            }).catch(function (error) {
                setMsg(msg, 'error', error && error.message ? error.message : '请求失败');
            }).finally(function () {
                setBusy(panel, false);
            });
        }

        panel.addEventListener('click', function (event) {
            var button = event.target.closest('[data-iso-action]');
            if (!button) return;
            var id = button.getAttribute('data-iso-id');
            var name = button.getAttribute('data-iso-name') || id;
            if (button.getAttribute('data-iso-action') === 'attach') {
                confirmDialog('挂载 ISO', '确认将「' + name + '」挂载到本实例？可在开机引导界面选择安装或救援。', '挂载', false, function () {
                    call('isoAttach', { iso_id: id }, 'ISO 已挂载');
                });
            } else {
                confirmDialog('卸载 ISO', '确认从本实例卸载「' + name + '」？', '卸载', true, function () {
                    call('isoDetach', {}, 'ISO 已卸载');
                });
            }
        });

        load(false);
    };

    /* ---------------- 重装系统 ---------------- */
    PANELS.reinstall = function () {
        var panel = app.querySelector('[data-panel="reinstall"]');
        if (!panel) return;
        var msg = panel.querySelector('[data-msg]');
        var sel = panel.querySelector('[data-rei-template]');
        var modeSel = panel.querySelector('[data-rei-mode]');
        var runBtn = panel.querySelector('[data-rei-run]');
        var currentEl = panel.querySelector('[data-rei-current]');
        var currentTemplate = '';

        function loadTemplates() {
            api('reinstallTemplates', {}).then(function (res) {
                if (!res.success || !Array.isArray(res.data.templates)) {
                    sel.innerHTML = '<option value="">暂无可用模板</option>';
                    runBtn.disabled = true;
                    return;
                }
                currentTemplate = String(res.data.current_template || '');
                if (currentEl) currentEl.textContent = currentTemplate || '-';
                var options = res.data.templates.map(function (t) {
                    var id = escapeHtml(t.id);
                    var arch = escapeHtml(t.arch || '');
                    var name = escapeHtml(t.name || t.id) + (arch ? ' (' + arch + ')' : '');
                    var selected = (String(t.id) === currentTemplate) ? ' selected' : '';
                    return '<option value="' + id + '"' + selected + '>' + name + '</option>';
                }).join('');
                sel.innerHTML = options || '<option value="">暂无可用模板</option>';
                runBtn.disabled = sel.options.length < 1;
            }).catch(function (error) {
                setMsg(msg, 'error', error && error.message ? error.message : '请求失败');
                sel.innerHTML = '<option value="">暂无可用模板</option>';
                runBtn.disabled = true;
            });
        }

        runBtn.addEventListener('click', function () {
            var templateId = sel.value;
            if (!templateId) {
                setMsg(msg, 'error', '请先选择系统模板');
                return;
            }
            var templateName = sel.options[sel.selectedIndex] ? sel.options[sel.selectedIndex].text : templateId;
            var mode = modeSel.value || 'full';
            var modeText = mode === 'system' ? '仅重装系统盘（保留数据盘）' : '完整重装（清空系统盘）';
            confirmDialog('重装系统', '确认将实例重装为「' + templateName + '」，范围：' + modeText + '？该操作不可逆！', '确认重装', true, function () {
                setBusy(panel, true);
                api('reinstall', { template_id: templateId, reinstall_mode: mode }).then(function (res) {
                    if (res.success) {
                        setMsg(msg, 'success', res.message || '重装任务已提交');
                    } else {
                        setMsg(msg, 'error', res.message || '重装失败');
                    }
                }).catch(function (error) {
                    setMsg(msg, 'error', error && error.message ? error.message : '请求失败');
                }).finally(function () {
                    setBusy(panel, false);
                });
            });
        });

        loadTemplates();
    };

    /* ---------------- 顶部控制台 / 电源 / 同步 ---------------- */
    app.querySelectorAll('[data-console]').forEach(function (button) {
        button.addEventListener('click', function () {
            var kind = button.getAttribute('data-console') === 'vnc' ? 'vnc' : 'webssh';
            button.disabled = true;
            api(kind, {}).then(function (res) {
                if (res.success && res.data && res.data.url) {
                    var opened = window.open(res.data.url, '_blank', 'noopener');
                    if (!opened) showGlobal('error', '浏览器拦截了新窗口，请允许弹出窗口后重试。');
                } else {
                    showGlobal('error', res.message || '控制台打开失败');
                }
            }).catch(function (error) {
                showGlobal('error', error && error.message ? error.message : '请求失败');
            }).finally(function () {
                button.disabled = false;
            });
        });
    });

    // 危险操作列表 —— 触发前弹确认对话框
    var DANGEROUS_POWER = {
        powerHardOff: '硬关机将强制 kill 实例（等同于拔电源），未保存数据会丢失。确认继续？',
        rescueMode:   '进入救援模式将强制从救援 ISO 重启实例，原系统暂时不可用。确认继续？',
        rescueExit:   '退出救援模式将卸载救援 ISO 并重启回到原磁盘系统。确认继续？'
    };

    app.querySelectorAll('[data-power]').forEach(function (button) {
        button.addEventListener('click', function () {
            var func = button.getAttribute('data-power');
            var doCall = function () {
                button.disabled = true;
                api(func, {}).then(function (res) {
                    showGlobal(res.success ? 'success' : 'error', res.message || (res.success ? '操作成功' : '操作失败'));
                }).catch(function (error) {
                    showGlobal('error', error && error.message ? error.message : '请求失败');
                }).finally(function () {
                    button.disabled = false;
                });
            };
            if (DANGEROUS_POWER[func]) {
                confirmDialog('操作确认', DANGEROUS_POWER[func], '确认执行', true, doCall);
            } else {
                doCall();
            }
        });
    });

    var syncBtn = app.querySelector('[data-sync]');
    if (syncBtn) {
        syncBtn.addEventListener('click', function () {
            syncBtn.disabled = true;
            api('sync', {}).then(function (res) {
                showGlobal(res.success ? 'success' : 'error', res.message || (res.success ? '状态已同步' : '同步失败'));
            }).catch(function (error) {
                showGlobal('error', error && error.message ? error.message : '请求失败');
            }).finally(function () {
                syncBtn.disabled = false;
            });
        });
    }

    activateTab('info');
})();
</script>
{/literal}