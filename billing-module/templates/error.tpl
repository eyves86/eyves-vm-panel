{* 错误页：不需要卡片外壳，面板本身即可 *}
<div class="ev-note ev-note--danger">
  <i class="bi bi-exclamation-circle-fill"></i>
  <div>{if $message}{$message|escape:'html'}{else}{$lang.operation_failed}{/if}</div>
</div>
