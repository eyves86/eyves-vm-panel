{* 设置：改主机名 / 重装系统 / 重置流量 *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.rename}</span>
</div>

{if $vm.suspended}<div class="ev-note ev-note--warn">{$lang.suspended_tip}</div>{/if}

<form data-ev-form="hostname">
  <div class="ev-row2">
    <div class="ev-field">
      <label>{$lang.hostname}</label>
      <input type="text" class="ev-input" name="hostname" value="{$vm.hostname|escape:'html'}" maxlength="63" pattern="[a-z0-9-]+">
      <div class="ev-hint">{$lang.hostname_hint}</div>
    </div>
  </div>
  <button type="submit" class="ev-btn ev-btn--primary" {if $vm.suspended || $vm.locked}disabled{/if}>{$lang.save}</button>
</form>

{* ---------------- 重装系统 ---------------- *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.reinstallos}</span>
</div>

<div class="ev-note ev-note--danger">
  <i class="bi bi-exclamation-triangle-fill"></i>
  <div>{$lang.reinstall_tip}</div>
</div>

<form data-ev-form="reinstall">
  <div class="ev-row2">
    <div class="ev-field">
      <label>{$lang.operatingsystem}</label>
      {* 镜像清单已由后端按本实例的运行时过滤，这里不做任何运行时判断 *}
      <select class="ev-select" name="image">
        {foreach $data.images as $img}
          <option value="{$img.id|escape:'html'}" {if $img.id eq $vm.template_id}selected{/if}>
            {$img.name|escape:'html'}{if !$img.downloaded}{$lang.image_not_downloaded}{/if}
          </option>
        {/foreach}
      </select>
      <div class="ev-hint">{$lang.image_runtime_filtered|string_format:$vm.runtime_label}</div>
    </div>
  </div>
  <button type="submit" class="ev-btn ev-btn--danger" {if $vm.suspended || $vm.locked}disabled{/if}>
    {$lang.reinstallos}
  </button>
</form>

{* ---------------- 重置流量 ---------------- *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.reset_traffic}</span>
</div>
<button type="button" class="ev-btn" data-ev-resettraffic>{$lang.reset_traffic}</button>

<script>
(function () {
  var hf = document.querySelector('[data-ev-form="hostname"]');
  if (hf) {
    hf.addEventListener('submit', function (e) {
      e.preventDefault();
      var btn = hf.querySelector('button[type=submit]');
      EvClient.busy(btn, true);
      EvClient.post('rename', { hostname: hf.hostname.value.trim() }).then(function (res) {
        EvClient.busy(btn, false);
        EvClient.toast(res.message || (res.status === 'success' ? EvClient.lang.operation_success : EvClient.lang.operation_failed),
                       res.status === 'success' ? 'success' : 'error');
        if (res.status === 'success') { setTimeout(function () { location.reload(); }, 800); }
      });
    });
  }

  var rf = document.querySelector('[data-ev-form="reinstall"]');
  if (rf) {
    rf.addEventListener('submit', function (e) {
      e.preventDefault();
      var image = rf.image.value;
      if (!image) { EvClient.toast(EvClient.lang.select_image, 'error'); return; }
      // 重装是不可逆操作：要求输入实例名确认
      EvClient.confirmByName({
        title: EvClient.lang.reinstallos,
        warning: EvClient.lang.reinstall_tip,
        expect: '{$vm.name|escape:'javascript'}',
        label: EvClient.lang.reinstall_confirm,
        okText: EvClient.lang.reinstallos,
        onPass: function (btn) {
          EvClient.busy(btn, true);
          EvClient.post('reinstall', { image: image }).then(function (res) {
            if (res.status === 'success') { location.reload(); }
            else { EvClient.busy(btn, false); EvClient.toast(res.message, 'error'); }
          });
        }
      });
    });
  }

  var rt = document.querySelector('[data-ev-resettraffic]');
  if (rt) {
    rt.addEventListener('click', function () {
      if (!confirm(EvClient.lang.warn_irreversible)) { return; }
      EvClient.busy(rt, true);
      EvClient.post('resetTraffic').then(function (res) {
        EvClient.busy(rt, false);
        EvClient.toast(res.status === 'success' ? EvClient.lang.reset_traffic_ok : res.message,
                       res.status === 'success' ? 'success' : 'error');
      });
    });
  }
})();
</script>
