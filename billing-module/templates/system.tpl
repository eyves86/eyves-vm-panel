{* 全局容器：Toast 挂载点。弹层由 javascript.tpl 动态创建（避免每页重复 DOM）。 *}
<div class="ev-toasts" role="status" aria-live="polite"></div>

{* 页签内联脚本可把初始化函数挂到这里，由主壳统一在 DOM 就绪后调用 *}
<script>
  window.__evPageInit = window.__evPageInit || [];
</script>
