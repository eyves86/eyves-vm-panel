{* 数据盘：EyvesCloud 为单块数据盘，只支持扩容（面板语义：磁盘不缩容） *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.datadisk}</span>
</div>

{if $vm.data_disk_gb gt 0}
  <div class="ev-kv" style="margin-bottom:20px">
      <div class="ev-kv__item"><div class="ev-kv__k">{$lang.datadisk}</div><div class="ev-kv__v">{$vm.data_disk_gb} <span style="color:var(--ev-ink-3);font-size:12px">GB</span></div></div>
      <div class="ev-kv__item"><div class="ev-kv__k">{$lang.status}</div><div class="ev-kv__v"><span class="ev-chip ev-chip--running">{$lang.mounted}</span></div></div>
  </div>
{else}
  <div class="ev-empty"><i class="bi bi-hdd"></i><span>{$lang.datadisk_none}</span></div>
{/if}

{if $vm.suspended}
  <div class="ev-note ev-note--warn">{$lang.suspended_tip}</div>
{/if}

<form data-ev-form="datadisk" style="margin-top:8px">
  <div class="ev-row2">
    <div class="ev-field">
      <label>{$lang.datadisk} (GB)</label>
      <input type="number" class="ev-input" name="data_disk_gb" min="{$vm.data_disk_gb}"
             value="{$vm.data_disk_gb}" step="1">
      <div class="ev-hint">{$lang.datadisk_grow_only|string_format:$vm.data_disk_gb}</div>
    </div>
  </div>
  <button type="submit" class="ev-btn ev-btn--primary" {if $vm.suspended || $vm.locked}disabled{/if}>{$lang.save}</button>
</form>

<script>
(function () {
  var f = document.querySelector('[data-ev-form="datadisk"]');
  if (!f) { return; }
  f.addEventListener('submit', function (e) {
    e.preventDefault();
    var btn = f.querySelector('button[type=submit]');
    EvClient.busy(btn, true);
    EvClient.post('resizeDataDisk', { data_disk_gb: f.data_disk_gb.value }).then(function (res) {
      EvClient.busy(btn, false);
      EvClient.toast(res.message || (res.status === 'success' ? EvClient.lang.operation_success : EvClient.lang.operation_failed),
                     res.status === 'success' ? 'success' : 'error');
    });
  });
})();
</script>
