{* 快照：创建 / 还原 / 删除。挂起时禁止创建与还原（面板同策略）。 *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.snapshots}</span>
  <div class="ev-btnrow">
    {if $vm.snapshot_limit gt 0}
      <span style="font-size:12px;color:var(--ev-ink-2)">
        {$lang.snapshot_limit} {$data.count} / {$vm.snapshot_limit}
      </span>
    {/if}
    <button type="button" class="ev-btn ev-btn--sm ev-btn--primary" data-ev-snapnew
            {if $vm.suspended || $vm.locked}disabled{/if}>
      <i class="bi bi-camera"></i> {$lang.snapshot_create}
    </button>
  </div>
</div>

{if $vm.suspended}<div class="ev-note ev-note--warn">{$lang.suspended_tip}</div>{/if}

{if $data.snapshots}
<div class="ev-tablewrap"><table class="ev-table">
  <thead>
    <tr>
      <th>{$lang.snapshot_name}</th>
      <th>{$lang.size}</th>
      <th>{$lang.created}</th>
      <th>{$lang.status}</th>
      <th class="ev-r">{$lang.action}</th>
    </tr>
  </thead>
  <tbody>
    {foreach $data.snapshots as $s}
      <tr>
        <td data-label="{$lang.snapshot_name}">{$s.name|escape:'html'|default:'—'}</td>
        <td data-label="{$lang.size}" class="is-mono">{if $s.size_mb}{$s.size_mb} MB{else}—{/if}</td>
        <td data-label="{$lang.created}" class="is-mono">{$s.created_at|default:'—'}</td>
        <td data-label="{$lang.status}"><span class="ev-chip ev-chip--ghost">{$s.status|escape:'html'}</span></td>
        <td data-label="{$lang.action}" class="ev-r">
          <button type="button" class="ev-btn ev-btn--sm" data-ev-snaprestore="{$s.id|escape:'html'}"
                  {if $vm.suspended || $vm.locked}disabled{/if}>{$lang.restore}</button>
          <button type="button" class="ev-btn ev-btn--sm ev-btn--danger" data-ev-snapdel="{$s.id|escape:'html'}"
                  {if $vm.locked}disabled{/if}>{$lang.delete}</button>
        </td>
      </tr>
    {/foreach}
  </tbody>
</table></div>
{else}
  <div class="ev-empty"><i class="bi bi-camera"></i><span>{$lang.no_snapshots}</span></div>
{/if}

<script>
(function () {
  var newBtn = document.querySelector('[data-ev-snapnew]');
  if (newBtn) {
    newBtn.addEventListener('click', function () {
      var html = '<div class="ev-field"><label>' + EvClient.lang.snapshot_name +
        '</label><input type="text" class="ev-input" data-ev-snapname maxlength="64"></div>';
      EvClient.confirmModal({
        title: EvClient.lang.snapshot_create, body: html, okText: EvClient.lang.create,
        afterRender: function (o) { o.querySelector('[data-ev-snapname]').focus(); },
        onConfirm: function (btn) {
          var name = document.querySelector('[data-ev-snapname]').value.trim();
          EvClient.busy(btn, true);
          EvClient.post('createSnapshot', { name: name }).then(function (res) {
            if (res.status === 'success') { location.reload(); }
            else { EvClient.busy(btn, false); EvClient.toast(res.message, 'error'); }
          });
        }
      });
    });
  }

  document.querySelectorAll('[data-ev-snaprestore]').forEach(function (b) {
    b.addEventListener('click', function () {
      EvClient.confirmModal({
        title: EvClient.lang.snapshot_restore,
        body: '<div class="ev-note ev-note--danger">' + EvClient.lang.warn_irreversible + '</div>',
        okText: EvClient.lang.restore, danger: true,
        onConfirm: function (btn) {
          EvClient.busy(btn, true);
          EvClient.post('restoreSnapshot', { sid: b.dataset.evSnaprestore }).then(function (res) {
            if (res.status === 'success') { location.reload(); }
            else { EvClient.busy(btn, false); EvClient.toast(res.message, 'error'); }
          });
        }
      });
    });
  });

  document.querySelectorAll('[data-ev-snapdel]').forEach(function (b) {
    b.addEventListener('click', function () {
      if (!confirm(EvClient.lang.confirm_delete)) { return; }
      EvClient.busy(b, true);
      EvClient.post('deleteSnapshot', { sid: b.dataset.evSnapdel }).then(function (res) {
        if (res.status === 'success') { location.reload(); }
        else { EvClient.busy(b, false); EvClient.toast(res.message, 'error'); }
      });
    });
  });
})();
</script>
