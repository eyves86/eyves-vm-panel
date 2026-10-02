{* ==========================================================================
   页签导航
   可见性完全由能力位决定（LXC 不渲染 ISO、无配额不渲染对应页签）。
   带图标是为了扫读效率：十几个页签纯文字排一列很难快速定位。
   ========================================================================== *}
{assign var="evBase" value="`$webRoot`/clientarea.php?action=productdetails&id=`$serviceid`"}

<div class="ev-tabs-wrap" data-ev-tabswrap>
<nav class="ev-tabs" role="tablist" aria-label="{$lang.instance}">
  <a href="{$evBase}" role="tab" class="{if $page eq 'base'}is-active{/if}"
     {if $page eq 'base'}aria-current="page"{/if}>
    <i class="bi bi-grid-1x2"></i>{$lang.overview}
  </a>
  <a href="{$evBase}&page=monitor" role="tab" class="{if $page eq 'monitor'}is-active{/if}"
     {if $page eq 'monitor'}aria-current="page"{/if}>
    <i class="bi bi-activity"></i>{$lang.monitor}
  </a>
  <a href="{$evBase}&page=network" role="tab" class="{if $page eq 'network'}is-active{/if}"
     {if $page eq 'network'}aria-current="page"{/if}>
    <i class="bi bi-diagram-3"></i>{$lang.network}
  </a>

  {if $caps.port_map}
    <a href="{$evBase}&page=portmap" role="tab" class="{if $page eq 'portmap'}is-active{/if}"
       {if $page eq 'portmap'}aria-current="page"{/if}>
      <i class="bi bi-signpost-split"></i>{$lang.portmap}
    </a>
  {/if}

  {if $caps.snapshot}
    <a href="{$evBase}&page=snapshots" role="tab" class="{if $page eq 'snapshots'}is-active{/if}"
       {if $page eq 'snapshots'}aria-current="page"{/if}>
      <i class="bi bi-camera"></i>{$lang.snapshots}
    </a>
  {/if}

  {if $caps.backup}
    <a href="{$evBase}&page=backups" role="tab" class="{if $page eq 'backups'}is-active{/if}"
       {if $page eq 'backups'}aria-current="page"{/if}>
      <i class="bi bi-box-seam"></i>{$lang.backups}
    </a>
  {/if}

  {if $caps.firewall}
    <a href="{$evBase}&page=securitys" role="tab" class="{if $page eq 'securitys'}is-active{/if}"
       {if $page eq 'securitys'}aria-current="page"{/if}>
      <i class="bi bi-shield-check"></i>{$lang.securitys}
    </a>
  {/if}

  {if $caps.data_disk}
    <a href="{$evBase}&page=drive" role="tab" class="{if $page eq 'drive'}is-active{/if}"
       {if $page eq 'drive'}aria-current="page"{/if}>
      <i class="bi bi-device-hdd"></i>{$lang.datadisk}
    </a>
  {/if}

  {* ISO 与救援：只有 KVM 会渲染 *}
  {if $caps.iso_mount}
    <a href="{$evBase}&page=iso" role="tab" class="{if $page eq 'iso'}is-active{/if}"
       {if $page eq 'iso'}aria-current="page"{/if}>
      <i class="bi bi-disc"></i>{$lang.iso}
    </a>
  {/if}

  <a href="{$evBase}&page=sshkey" role="tab" class="{if $page eq 'sshkey'}is-active{/if}"
     {if $page eq 'sshkey'}aria-current="page"{/if}>
    <i class="bi bi-key"></i>{$lang.sshkey}
  </a>

  {if $caps.reset_pw}
    <a href="{$evBase}&page=password" role="tab" class="{if $page eq 'password'}is-active{/if}"
       {if $page eq 'password'}aria-current="page"{/if}>
      <i class="bi bi-lock"></i>{$lang.password}
    </a>
  {/if}

  <a href="{$evBase}&page=crons" role="tab" class="{if $page eq 'crons'}is-active{/if}"
     {if $page eq 'crons'}aria-current="page"{/if}>
    <i class="bi bi-clock-history"></i>{$lang.crons}
  </a>
  <a href="{$evBase}&page=tasks" role="tab" class="{if $page eq 'tasks'}is-active{/if}"
     {if $page eq 'tasks'}aria-current="page"{/if}>
    <i class="bi bi-list-task"></i>{$lang.tasks}
  </a>
  <a href="{$evBase}&page=setting" role="tab" class="{if $page eq 'setting'}is-active{/if}"
     {if $page eq 'setting'}aria-current="page"{/if}>
    <i class="bi bi-sliders"></i>{$lang.setting}
  </a>
</nav>
</div>
