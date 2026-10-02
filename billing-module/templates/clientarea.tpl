{* ==========================================================================
   EyvesCloud 客户区主壳
   结构：身份区 → KPI 指标条 → 页签 → 内容
   所有运行时差异由 $caps 能力位驱动，本文件及子模板**不得**判断 runtime。
   ========================================================================== *}
<link rel="stylesheet" href="{$assets}css/eyves.css?v={$evVersion}">
<link rel="stylesheet" href="{$assets}css/bootstrap-icons.css">
<link rel="stylesheet" href="{$assets}css/flag-icons.min.css">
<link rel="stylesheet" href="{$assets}css/font-os/css/font-os.css">

<div class="ev-panel" data-runtime="{$vm.runtime}" data-status="{$vm.status}" id="evPanel">

  {if $pageAlert}
    <div class="ev-note ev-note--{$pageAlert.type|default:'info'}">
      <i class="bi bi-info-circle-fill"></i>
      <div>{$pageAlert.text}</div>
    </div>
  {/if}

  {* ---------- 身份区 ---------- *}
  {include file="./header.tpl"}

  {* ---------- KPI 指标条 ---------- *}
  {include file="./header-content.tpl"}

  {* ---------- 页签 + 内容 ---------- *}
  <section class="ev-body">
    {include file="./nav.tpl"}
    <div class="ev-pane" role="tabpanel">
      {include file="./tabs/{$page}.tpl"}
    </div>
  </section>

  <div class="ev-toasts" role="status" aria-live="polite"></div>
</div>

{include file="./javascript.tpl"}
