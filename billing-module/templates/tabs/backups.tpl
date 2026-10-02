{* 备份：创建 / 还原 / 删除。EyvesCloud 的备份能力原魔方模块没有，属新增。 *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.backups}</span>
  <button type="button" class="ev-btn ev-btn--sm ev-btn--primary" data-ev-baknew
          {if $vm.suspended || $vm.locked}disabled{/if}>
    <i class="bi bi-archive"></i> {$lang.backup_create}
  </button>
</div>

{if $vm.suspended}<div class="ev-note ev-note--warn">{$lang.suspended_tip}</div>{/if}

{if $data.backups}
<div class="ev-tablewrap"><table class="ev-table">
  <thead>
    <tr>
      <th>{$lang.instance}</th>
      <th>{$lang.size}</th>
      <th>{$lang.created}</th>
      <th>{$lang.status}</th>
      <th class="ev-r">{$lang.action}</th>
    </tr>
  </thead>
  <tbody>
    {foreach $data.backups as $b}
      <tr>
        <td data-label="{$lang.instance}" class="is-mono">{$b.id|escape:'html'}</td>
        <td data-label="{$lang.size}" class="is-mono">{if $b.size_mb}{$b.size_mb} MB{else}—{/if}</td>
        <td data-label="{$lang.created}" class="is-mono">{$b.created_at|default:'—'}</td>
        <td data-label="{$lang.status}"><span class="ev-chip ev-chip--ghost">{$b.status|escape:'html'}</span></td>
        <td data-label="{$lang.action}" class="ev-r">
          <button type="button" class="ev-btn ev-btn--sm" data-ev-bakrestore="{$b.id|escape:'html'}"
                  {if $vm.suspended || $vm.locked}disabled{/if}>{$lang.restore}</button>
          <button type="button" class="ev-btn ev-btn--sm ev-btn--danger" data-ev-bakdel="{$b.id|escape:'html'}"
                  {if $vm.locked}disabled{/if}>{$lang.delete}</button>
        </td>
      </tr>
    {/foreach}
  </tbody>
</table></div>
{else}
  <div class="ev-empty"><i class="bi bi-archive"></i><span>{$lang.no_backups}</span></div>
{/if}

<script>
(function () {
  var nb = document.querySelector('[data-ev-baknew]');
  if (nb) {
    nb.addEventListener('click', function () {
      EvClient.confirmModal({
        title: EvClient.lang.backup_create,
        body: '<div style="font-size:13px">' + EvClient.lang.loading + '</div>',
        okText: EvClient.lang.create,
        onConfirm: function (btn) {
          EvClient.busy(btn, true);
          EvClient.post('createBackup').then(function (res) {
            if (res.status === 'success') { location.reload(); }
            else { EvClient.busy(btn, false); EvClient.toast(res.message, 'error'); }
          });
        }
      });
    });
  }

  document.querySelectorAll('[data-ev-bakrestore]').forEach(function (b) {
    b.addEventListener('click', function () {
      EvClient.confirmModal({
        title: EvClient.lang.restore,
        body: '<div class="ev-note ev-note--danger">' + EvClient.lang.warn_irreversible + '</div>',
        okText: EvClient.lang.restore, danger: true,
        onConfirm: function (btn) {
          EvClient.busy(btn, true);
          EvClient.post('restoreBackup', { bid: b.dataset.evBakrestore }).then(function (res) {
            if (res.status === 'success') { location.reload(); }
            else { EvClient.busy(btn, false); EvClient.toast(res.message, 'error'); }
          });
        }
      });
    });
  });

  document.querySelectorAll('[data-ev-bakdel]').forEach(function (b) {
    b.addEventListener('click', function () {
      if (!confirm(EvClient.lang.confirm_delete)) { return; }
      EvClient.busy(b, true);
      EvClient.post('deleteBackup', { bid: b.dataset.evBakdel }).then(function (res) {
        if (res.status === 'success') { location.reload(); }
        else { EvClient.busy(b, false); EvClient.toast(res.message, 'error'); }
      });
    });
  });
})();
</script>
