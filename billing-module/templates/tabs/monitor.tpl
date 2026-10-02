{* 监控：时间范围切换 + 四个图表。数据来自 /instances/{id}/metrics *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.monitor}</span>
  {* 无 class 的内联 flex 在窄屏不会换行，会把整行撑出屏幕 *}
  <div class="ev-btnrow">
    {foreach $ranges as $r}
      <a class="ev-btn ev-btn--sm {if $r.active}ev-btn--primary{/if}"
         href="{$webRoot}/clientarea.php?action=productdetails&id={$serviceid}&page=monitor&range={$r.key}">{$r.label}</a>
    {/foreach}
    <button type="button" class="ev-btn ev-btn--sm" data-ev-action="refresh"
            title="{$lang.refresh}" aria-label="{$lang.refresh}"><i class="bi bi-arrow-clockwise"></i></button>
  </div>
</div>

{if $data.hasData}
  {* 图表容器：flot 在 assets/js 里，由本页初始化 *}
  <div class="ev-row2" style="grid-template-columns:repeat(auto-fit,minmax(320px,1fr));gap:18px">
    {foreach $charts as $c}
      <div>
        <div style="font-size:13px;font-weight:500;margin-bottom:8px">{$c.label}
          <span style="float:right;font-family:var(--ev-mono);color:var(--ev-ink-2);font-weight:400">
            {$c.current}{$c.unit}
          </span>
        </div>
        <div id="ev-chart-{$c.key}" style="height:170px;background:var(--ev-bg-sub);border-radius:var(--ev-r-sm)"></div>
      </div>
    {/foreach}
  </div>

  <script>
  (function () {
    var SERIES = {$chartsJson};
    function draw() {
      if (!window.jQuery || !jQuery.plot) { return; }
      SERIES.forEach(function (c) {
        var el = document.getElementById('ev-chart-' + c.key);
        if (!el || !c.points || !c.points.length) { return; }
        jQuery.plot(el, [{ data: c.points, color: c.color || '#2563eb', lines: { show: true, lineWidth: 2, fill: true, fillColor: (c.color || '#2563eb') + '18' } }], {
          grid: { borderWidth: 0, hoverable: false, margin: { top: 8, right: 8, bottom: 20, left: 38 } },
          xaxis: { mode: 'time', timezone: 'browser', ticks: 4, color: '#98a2b3', font: { size: 10 } },
          yaxis: { min: 0, ticks: 4, color: '#98a2b3', font: { size: 10 } }
        });
      });
    }
    if (window.jQuery && jQuery.plot) { draw(); }
    else {
      // flot 未加载时优雅降级为数值列表
      var s = document.createElement('script');
      s.src = '{$assets}js/jquery.flot.min.js';
      s.onload = function () {
        var t = document.createElement('script');
        t.src = '{$assets}js/jquery.flot.time.min.js';
        t.onload = draw;
        document.head.appendChild(t);
      };
      document.head.appendChild(s);
    }
  })();
  </script>
{else}
  <div class="ev-empty">
    <i class="bi bi-graph-up"></i>
    <div>{$lang.monitor_no_data}</div>
  </div>
{/if}
