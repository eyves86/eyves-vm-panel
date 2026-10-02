{* ==========================================================================
   指标条
   标签 + 数值横排。用 flex + 分隔线（grid 会在末尾留一块填不满的空格）。
   数值用等宽数字，单位降一档。
   ========================================================================== *}
<div class="ev-strip">
  <div class="ev-stat">
    <span class="ev-stat__k">{$lang.cpu}</span>
    <span class="ev-stat__v">{$vm.vcpu}<i>vCPU</i></span>
  </div>

  <div class="ev-stat">
    <span class="ev-stat__k">{$lang.memory}</span>
    <span class="ev-stat__v">
      {if $vm.memory_mb ge 1024}{$vm.memory_mb/1024|string_format:'%.1f'}<i>GB</i>{else}{$vm.memory_mb}<i>MB</i>{/if}
    </span>
  </div>

  <div class="ev-stat">
    <span class="ev-stat__k">{$lang.disk}</span>
    <span class="ev-stat__v">{$vm.disk_gb}<i>GB</i></span>
  </div>

  {* 数据盘：能力位控制；未挂载时弱化而不是留空 *}
  {if $caps.data_disk}
    <div class="ev-stat">
      <span class="ev-stat__k">{$lang.datadisk}</span>
      {if $vm.data_disk_gb gt 0}
        <span class="ev-stat__v">{$vm.data_disk_gb}<i>GB</i></span>
      {else}
        <span class="ev-stat__v is-dim">{$lang.datadisk_none}</span>
      {/if}
    </div>
  {/if}

  {* 单位放标签里：等宽分格时「200 /100 Mbps」会被 ellipsis 截断 *}
  <div class="ev-stat ev-stat--wide">
    <span class="ev-stat__k">{$lang.bandwidth} · Mbps</span>
    <span class="ev-stat__v">{$vm.bandwidth.down_mbps}/{$vm.bandwidth.up_mbps}</span>
  </div>

  {* 流量：数值 + 迷你进度条收在同一格 *}
  <div class="ev-stat ev-stat--grow">
    <span class="ev-stat__k">{$lang.traffic}</span>
    <span class="ev-stat__v">
      {$vm.traffic.used_gb}<i>/ {if $vm.traffic.quota_gb gt 0}{$vm.traffic.quota_gb} GB{else}{$lang.traffic_unlimited}{/if}</i>
      {if $vm.traffic.quota_gb gt 0}
        <i style="margin-left:auto">{$vm.traffic.percent}%</i>
      {/if}
    </span>
    {if $vm.traffic.quota_gb gt 0}
      <div class="ev-mini">
        <span class="{if $vm.traffic.percent ge 90}is-danger{elseif $vm.traffic.percent ge 70}is-warn{/if}"
              style="width:{$vm.traffic.percent}%"></span>
      </div>
    {/if}
  </div>

  <div class="ev-stat">
    <span class="ev-stat__k">{$lang.sshport}</span>
    <span class="ev-stat__v is-mono">{$vm.ssh_port|default:'—'}</span>
  </div>

  {* VNC 端口只有 KVM 有意义，能力位为 false 时整格不渲染 *}
  {if $caps.console_vnc}
    <div class="ev-stat">
      <span class="ev-stat__k">{$lang.vncport}</span>
      <span class="ev-stat__v is-mono">{$vm.vnc_port|default:'—'}</span>
    </div>
  {/if}

  <div class="ev-stat">
    <span class="ev-stat__k">{$lang.instanceid}</span>
    <span class="ev-stat__v is-mono">
      <span class="ev-copy">
        <span>{$vm.id|escape:'html'}</span>
        <button type="button" class="ev-copy__btn" data-ev-copy="{$vm.id|escape:'html'}"
                title="{$lang.copy}" aria-label="{$lang.copy}"><i class="bi bi-clipboard"></i></button>
      </span>
    </span>
  </div>
</div>

{if $vm.suspended}
  <div class="ev-note ev-note--warn" style="margin-top:16px">
    <i class="bi bi-exclamation-triangle-fill"></i>
    <div>{$lang.suspended_tip}{if $vm.suspend_reason}（{$vm.suspend_reason|escape:'html'}）{/if}</div>
  </div>
{/if}
