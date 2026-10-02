{* 网络：地址明细（键值行）+ 带宽调整。带宽属计费字段，挂起时面板会拒绝。 *}

<div class="ev-sec">
  <span class="ev-sec__t">{$lang.network}</span>
</div>

<div class="ev-kv">
  <div class="ev-kv__item">
    <div class="ev-kv__k">{$lang.mainip}</div>
    <div class="ev-kv__v is-mono">
      {if $vm.primary_ip}
        <span class="ev-copy">
          <span>{$vm.primary_ip|escape:'html'}</span>
          <button type="button" class="ev-copy__btn" data-ev-copy="{$vm.primary_ip|escape:'html'}"
                  title="{$lang.copy}" aria-label="{$lang.copy}"><i class="bi bi-clipboard"></i></button>
        </span>
      {else}<span style="color:var(--ev-ink-3)">—</span>{/if}
    </div>
  </div>
  <div class="ev-kv__item">
    <div class="ev-kv__k">{$lang.publicipv4}</div>
    <div class="ev-kv__v is-mono">
      {if $vm.public_ipv4}{foreach $vm.public_ipv4 as $ip}<div>{$ip|escape:'html'}</div>{/foreach}
      {else}<span style="color:var(--ev-ink-3)">—</span>{/if}
    </div>
  </div>
  <div class="ev-kv__item">
    <div class="ev-kv__k">{$lang.ipv6}</div>
    <div class="ev-kv__v is-mono">
      {if $vm.ipv6_addresses}{foreach $vm.ipv6_addresses as $ip}<div>{$ip|escape:'html'}</div>{/foreach}
      {else}<span style="color:var(--ev-ink-3)">—</span>{/if}
    </div>
  </div>
  <div class="ev-kv__item">
    <div class="ev-kv__k">{$lang.sshport}</div>
    <div class="ev-kv__v is-mono">{$vm.ssh_port|default:'—'}</div>
  </div>
  {if $caps.console_vnc}
    <div class="ev-kv__item">
      <div class="ev-kv__k">{$lang.vncport}</div>
      <div class="ev-kv__v is-mono">{$vm.vnc_port|default:'—'}</div>
    </div>
  {/if}
  <div class="ev-kv__item">
    <div class="ev-kv__k">MAC</div>
    <div class="ev-kv__v is-mono">{if $vm.mac_address}{$vm.mac_address|escape:'html'}{else}<span style="color:var(--ev-ink-3)">—</span>{/if}</div>
  </div>
</div>

<div class="ev-sec">
  <span class="ev-sec__t">{$lang.bandwidth}</span>
</div>

{if $vm.suspended}
  <div class="ev-note ev-note--warn">
    <i class="bi bi-exclamation-triangle-fill"></i>
    <div>{$lang.suspended_tip}</div>
  </div>
{/if}

<form data-ev-form="bandwidth">
  <div class="ev-row2">
    <div class="ev-field">
      <label>{$lang.bandwidth_down} (Mbps)</label>
      <input type="number" min="0" class="ev-input" name="down_mbps" value="{$vm.bandwidth.down_mbps}">
    </div>
    <div class="ev-field">
      <label>{$lang.bandwidth_up} (Mbps)</label>
      <input type="number" min="0" class="ev-input" name="up_mbps" value="{$vm.bandwidth.up_mbps}">
    </div>
  </div>
  <button type="submit" class="ev-btn ev-btn--primary" {if $vm.suspended}disabled{/if}>{$lang.save}</button>
</form>

<script>
(function () {
  var f = document.querySelector('[data-ev-form="bandwidth"]');
  if (!f) { return; }
  f.addEventListener('submit', function (e) {
    e.preventDefault();
    var btn = f.querySelector('button[type=submit]');
    EvClient.busy(btn, true);
    EvClient.post('updateBandwidth', {
      down_mbps: f.down_mbps.value,
      up_mbps: f.up_mbps.value
    }).then(function (res) {
      EvClient.busy(btn, false);
      EvClient.toast(res.message || (res.status === 'success' ? EvClient.lang.operation_success : EvClient.lang.operation_failed),
                     res.status === 'success' ? 'success' : 'error');
    });
  });
})();
</script>
