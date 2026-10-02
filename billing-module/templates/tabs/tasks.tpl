{* 任务记录：异步操作（创建/重装/快照…）的执行历史，运行中的可取消 *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.tasks}</span>
  <button type="button" class="ev-btn ev-btn--sm" data-ev-action="refresh"
        title="{$lang.refresh}" aria-label="{$lang.refresh}"><i class="bi bi-arrow-clockwise"></i> {$lang.refresh}</button>
</div>

{if $data.tasks}
<div class="ev-tablewrap"><table class="ev-table">
  <thead>
    <tr>
      <th>ID</th>
      <th>{$lang.task_action}</th>
      <th>{$lang.task_status}</th>
      <th>{$lang.task_progress}</th>
      <th>{$lang.task_message}</th>
      <th>{$lang.task_time}</th>
      <th class="ev-r">{$lang.action}</th>
    </tr>
  </thead>
  <tbody>
    {foreach $data.tasks as $t}
      <tr>
        <td data-label="ID" class="is-mono">{$t.id|escape:'html'}</td>
        <td data-label="{$lang.task_action}">{$t.action|escape:'html'|default:'—'}</td>
        <td data-label="{$lang.task_status}">
          {assign var="stCls" value="stopped"}
          {if $t.status eq 'success'}{assign var="stCls" value="running"}{/if}
          {if $t.status eq 'failed'}{assign var="stCls" value="error"}{/if}
          {if $t.status eq 'running' || $t.status eq 'pending'}{assign var="stCls" value="creating"}{/if}
          <span class="ev-chip ev-chip--{$stCls}">{$t.status|escape:'html'}</span>
        </td>
        <td data-label="{$lang.task_progress}" class="is-mono">{if $t.progress}{$t.progress}%{else}—{/if}</td>
        <td data-label="{$lang.task_message}" class="ev-cell-msg">{if $t.error}<span class="ev-cell-msg__err">{$t.error|escape:'html'}</span>{else}{$t.message|escape:'html'|default:'—'}{/if}</td>
        <td data-label="{$lang.task_time}" class="is-mono">{$t.created_at|default:'—'}</td>
        <td data-label="{$lang.action}" class="ev-r">
          {if $t.status eq 'running' || $t.status eq 'pending'}
            <button type="button" class="ev-btn ev-btn--sm ev-btn--danger" data-ev-taskcancel="{$t.id|escape:'html'}">{$lang.task_cancel}</button>
          {else}
            —
          {/if}
        </td>
      </tr>
    {/foreach}
  </tbody>
</table></div>
{else}
  <div class="ev-empty"><i class="bi bi-list-task"></i><span>{$lang.no_data}</span></div>
{/if}

<script>
(function () {
  document.querySelectorAll('[data-ev-taskcancel]').forEach(function (b) {
    b.addEventListener('click', function () {
      if (!confirm(EvClient.lang.confirm_delete)) { return; }
      EvClient.busy(b, true);
      EvClient.post('cancelTask', { taskid: b.dataset.evTaskcancel }).then(function (res) {
        if (res.status === 'success') { location.reload(); }
        else { EvClient.busy(b, false); EvClient.toast(res.message, 'error'); }
      });
    });
  });
})();
</script>
