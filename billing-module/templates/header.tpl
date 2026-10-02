{* ==========================================================================
   头部
   状态用 Cloudscape 的 StatusIndicator 形态：图标 + 文字（不是裸色点）。
   运行时用中性徽标。操作按重要性分层。
   ========================================================================== *}
{assign var="evStatusIcon" value="bi-question-circle"}
{assign var="evStatusCls" value=$vm.status}
{if $vm.suspended}{assign var="evStatusCls" value="suspended"}{elseif $vm.locked}{assign var="evStatusCls" value="stopped"}{/if}
{if $vm.status eq 'running'}{assign var="evStatusIcon" value="bi-check-circle-fill"}{/if}
{if $vm.status eq 'stopped'}{assign var="evStatusIcon" value="bi-stop-circle-fill"}{/if}
{if $vm.status eq 'creating'}{assign var="evStatusIcon" value="bi-arrow-repeat"}{/if}
{if $vm.status eq 'error'}{assign var="evStatusIcon" value="bi-exclamation-circle-fill"}{/if}
{if $vm.suspended}{assign var="evStatusIcon" value="bi-exclamation-triangle-fill"}{/if}

<div class="ev-hero">
  <div class="ev-hero__id">
    <h1 class="ev-hero__title">
      <span>{$vm.name|escape:'html'}</span>
      <span class="ev-chip ev-chip--{$vm.runtime}">{$vm.runtime_label}</span>
      <span class="ev-status ev-status--{$evStatusCls}">
        <i class="bi {$evStatusIcon}"></i>{$vm.status_label}{if $vm.locked}（{$lang.locked}）{/if}
      </span>
    </h1>

    <div class="ev-hero__sub">
      {if $vm.primary_ip}
        <span class="ev-meta ev-meta--mono">
          <i class="bi bi-hdd-network"></i>
          <span class="ev-copy">
            <span>{$vm.primary_ip|escape:'html'}</span>
            <button type="button" class="ev-copy__btn" data-ev-copy="{$vm.primary_ip|escape:'html'}"
                    title="{$lang.copy}" aria-label="{$lang.copy}"><i class="bi bi-clipboard"></i></button>
          </span>
        </span>
      {/if}
      {* 系统标识：等宽小图标，靠 .fa-os 的 SVG 背景渲染 *}
      {if $vm.template_id}
        <span class="ev-meta ev-meta--os" title="{$lang.operatingsystem}">
          {if $vm.os_family}<i class="fa-os fo-{$vm.os_family}"></i>{else}<i class="bi bi-hdd-stack"></i>{/if}
          {$vm.template_id|escape:'html'}
        </span>
      {/if}
      <span class="ev-meta" title="{$lang.node}">
        <i class="bi bi-geo-alt"></i>{$vm.node_name|escape:'html'}
      </span>
      {if $vm.expires_at}
        <span class="ev-meta" title="{$lang.expires}">
          <i class="bi bi-calendar3"></i>{$vm.expires_at|substr:0:10} {$lang.expires}
        </span>
      {/if}
      {if $vm.traffic.quota_gb gt 0 and $vm.traffic.percent ge 100}
        <span class="ev-status ev-status--error"><i class="bi bi-exclamation-circle-fill"></i>{$lang.traffic_error}</span>
      {/if}
    </div>
  </div>

  <div class="ev-actions">
    {* 控制台：类型与文案由后端下发（LXC→SSH 终端 / KVM→VNC 控制台） *}
    <button type="button" class="ev-btn ev-btn--primary"
            data-ev-action="console"
            {if $vm.status neq 'running' || $vm.suspended}disabled{/if}
            title="{if $vm.suspended}{$lang.console_suspended}{elseif $vm.status neq 'running'}{$lang.console_not_running}{else}{$vm.console.label}{/if}">
      <i class="bi {if $caps.console_vnc}bi-display{else}bi-terminal{/if}"></i>
      {$vm.console.label}
    </button>

    <button type="button" class="ev-btn" data-ev-action="dropdown" data-ev-menu="power">
      {if $vm.status eq 'running'}{$lang.shutdown}{else}{$lang.boot}{/if}
      <i class="bi bi-caret-down-fill" style="font-size:9px"></i>
    </button>

    <button type="button" class="ev-btn" data-ev-action="dropdown" data-ev-menu="reboot"
            title="{$lang.reboot}" {if $vm.status neq 'running'}disabled{/if}>
      <i class="bi bi-arrow-repeat"></i>
    </button>

    <a class="ev-btn ev-btn--quiet" href="{$webRoot}/clientarea.php?action=productdetails&id={$serviceid}&modop=custom&a=renew">{$lang.renewal}</a>
    <a class="ev-btn ev-btn--quiet" href="{$webRoot}/submitticket.php?step=2&relatedservice={$serviceid}" target="_blank">{$lang.submitticket}</a>
  </div>
</div>
