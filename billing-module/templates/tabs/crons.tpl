{* 定时任务：EyvesCloud 的对应能力是自动快照计划，此处只读展示 *}
<div class="ev-sec">
  <span class="ev-sec__t">{$lang.crons}</span>
  {if $data.schedule.enabled}
    <span class="ev-chip ev-chip--running">{$lang.enabled}</span>
  {else}
    <span class="ev-chip ev-chip--stopped">{$lang.disabled}</span>
  {/if}
</div>

<div class="ev-note ev-note--info">
  <i class="bi bi-info-circle-fill"></i>
  <div>{$lang.crons_tip}</div>
</div>

{if $data.schedule.enabled}
  <div class="ev-kv">
    <div class="ev-kv__item">
      <div class="ev-kv__k">{$lang.interval}</div>
      <div class="ev-kv__v is-mono">{$lang.every_n_hours|string_format:$data.schedule.interval_hours}</div>
    </div>
    <div class="ev-kv__item">
      <div class="ev-kv__k">{$lang.exec_time}</div>
      <div class="ev-kv__v is-mono">{$data.schedule.time|default:'—'}</div>
    </div>
    <div class="ev-kv__item">
      <div class="ev-kv__k">{$lang.last_run}</div>
      <div class="ev-kv__v is-mono">{$data.schedule.last_run|default:'—'}</div>
    </div>
    <div class="ev-kv__item">
      <div class="ev-kv__k">{$lang.next_run}</div>
      <div class="ev-kv__v is-mono">{$data.schedule.next_run|default:'—'}</div>
    </div>
  </div>
{else}
  <div class="ev-empty"><i class="bi bi-clock-history"></i><span>{$lang.no_data}</span></div>
{/if}
