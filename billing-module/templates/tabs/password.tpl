{* 重置密码。与面板策略一致：挂起态仍允许（管理恢复路径）。 *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.password}</span>
</div>

<div class="ev-note ev-note--info">
  <i class="bi bi-info-circle"></i>
  <div>{$lang.reset_password_tip}</div>
</div>

<form data-ev-form="password">
  <div class="ev-row2">
    <div class="ev-field">
      <label>{$lang.new_password}</label>
      <input type="text" class="ev-input" name="password" autocomplete="new-password"
             placeholder="{$lang.password_auto}" readonly
             onfocus="this.removeAttribute('readonly')">
      <div class="ev-hint">{$lang.password_rule}</div>
    </div>
  </div>
  <button type="submit" class="ev-btn ev-btn--primary">{$lang.confirm}</button>
</form>

<script>
(function () {
  var f = document.querySelector('[data-ev-form="password"]');
  if (!f) { return; }
  f.addEventListener('submit', function (e) {
    e.preventDefault();
    var btn = f.querySelector('button[type=submit]');
    var pw = f.password.value.trim();
    if (pw !== '' && pw.length < 8) { EvClient.toast(EvClient.lang.password_rule, 'error'); return; }

    var send = function (b) {
      EvClient.busy(b, true);
      EvClient.post('resetPassword', { password: pw }).then(function (res) {
        EvClient.busy(b, false);
        if (res.status !== 'success') { EvClient.toast(res.message, 'error'); return; }
        var newPw = (res.data && res.data.password) ? res.data.password : pw;
        EvClient.confirmModal({
          title: EvClient.lang.password_reset_ok,
          body: '<div class="ev-field"><label>' + EvClient.lang.new_password + '</label>' +
                '<input type="text" class="ev-input is-mono" value="' + newPw + '" readonly onclick="this.select()"></div>' +
                '<div class="ev-hint">{$lang.password_save_now}</div>',
          okText: EvClient.lang.close
        });
      });
    };

    EvClient.confirmModal({
      title: EvClient.lang.password,
      body: '<div class="ev-note ev-note--warn">' + EvClient.lang.warn_irreversible + '</div>',
      okText: EvClient.lang.confirm, danger: true,
      onConfirm: function (b) { EvClient.closeModal(); send(b); }
    });
  });
})();
</script>
