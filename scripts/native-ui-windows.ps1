param(
  [Parameter(Mandatory=$true)][int]$AppProcessId,
  [Parameter(Mandatory=$true)][string]$RequestBase64
)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class NativeInput {
    [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
    [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] public static extern IntPtr GetSystemMenu(IntPtr window, bool revert);
    [DllImport("user32.dll")] public static extern uint GetMenuState(IntPtr menu, uint item, uint flags);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window, uint message, IntPtr wparam, IntPtr lparam);
}
'@
$request = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RequestBase64)) | ConvertFrom-Json
$response = @{}
$scope = [System.Windows.Automation.TreeScope]::Descendants
$all = [System.Windows.Automation.Condition]::TrueCondition
$owner = New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ProcessIdProperty, $AppProcessId)

function Get-Windows {
  return [System.Windows.Automation.AutomationElement]::RootElement.FindAll([System.Windows.Automation.TreeScope]::Children, $owner)
}

function Find-Control([string]$Name, [string]$Kind = '') {
  $deadline = (Get-Date).AddSeconds(15)
  do {
    foreach ($window in (Get-Windows)) {
      $elements = @($window) + @($window.FindAll($scope, $all))
      foreach ($element in $elements) {
        try {
          $current = $element.Current
          if ($current.Name -match $Name -and (!$Kind -or $current.ControlType.ProgrammaticName -eq "ControlType.$Kind") -and !$current.IsOffscreen -and $current.IsEnabled) { return $element }
        } catch [System.Windows.Automation.ElementNotAvailableException] { }
      }
    }
    Start-Sleep -Milliseconds 100
  } while ((Get-Date) -lt $deadline)
  $tree = foreach ($window in (Get-Windows)) {
    foreach ($element in @($window) + @($window.FindAll($scope, $all))) {
      try { "$($element.Current.ControlType.ProgrammaticName):$($element.Current.Name):$($element.Current.AutomationId)" } catch { }
    }
  }
  throw "Native control '$Name' ($Kind) not found: $($tree -join '; ')"
}

function Focus-Control($element) {
  $current = $element.Current
  if ($current.ControlType -eq [System.Windows.Automation.ControlType]::Window) {
    $handle = [IntPtr]$current.NativeWindowHandle
    if ($handle -eq [IntPtr]::Zero) { throw "Native window '$($current.Name)' has no handle" }
    [void][NativeInput]::SetForegroundWindow($handle)
    $deadline = (Get-Date).AddSeconds(3)
    while ([NativeInput]::GetForegroundWindow() -ne $handle) {
      if ((Get-Date) -ge $deadline) { throw "Native window '$($current.Name)' did not become foreground" }
      Start-Sleep -Milliseconds 50
    }
  } else {
    try { $element.SetFocus() }
    catch { throw "Native control '$($current.Name)' ($($current.ControlType.ProgrammaticName)) cannot receive focus: $_" }
  }
}

function Click-Control($element) {
  $element.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern).Invoke()
}

switch ($request.action) {
  'key' {
    Focus-Control (Find-Control '^garminctl$' 'Window')
    if ($request.key -ne 'F5') { throw 'Unsupported native key' }
    [System.Windows.Forms.SendKeys]::SendWait('{F5}')
  }
  'close' {
    $window = Find-Control '^garminctl$' 'Window'
    if (![NativeInput]::PostMessage([IntPtr]$window.Current.NativeWindowHandle, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero)) { throw 'WM_CLOSE failed' }
  }
  'save' {
    $dialog = Find-Control '^Save file$' 'Window'
    Focus-Control $dialog
    $inputBox = Find-Control '^File name' 'Edit'
    $value = $inputBox.GetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern)
    Focus-Control $inputBox
    [System.Windows.Forms.Clipboard]::SetText([string]$request.path)
    [System.Windows.Forms.SendKeys]::SendWait('^a')
    [System.Windows.Forms.SendKeys]::SendWait('^v')
    if ($value.Current.Value -ne [string]$request.path) { throw "Native Save filename differs: '$($value.Current.Value)'" }
    $response = @{ enteredPath = $value.Current.Value; saveButton = $true }
    [System.Windows.Forms.SendKeys]::SendWait('{TAB}')
    $response.committedName = $value.Current.Value
    Click-Control (Find-Control '^Save$' 'Button')
  }
  'cancel-save' {
    [void](Find-Control '^Save file$' 'Window')
    Click-Control (Find-Control '^Cancel$' 'Button')
  }
  'replace' {
    if ($request.choice -eq 'yes') {
      Click-Control (Find-Control '^Yes$' 'Button')
    } elseif ($request.choice -eq 'no') {
      Click-Control (Find-Control '^No$' 'Button')
      Focus-Control (Find-Control '^Save file$' 'Window')
      [System.Windows.Forms.SendKeys]::SendWait('{ESC}')
    } elseif ($request.choice -in @('escape', 'close')) {
      $dialog = Find-Control '^Confirm Save As$' 'Window'
      Focus-Control $dialog
      $confirmationHandle = [IntPtr]$dialog.Current.NativeWindowHandle
      $menu = [NativeInput]::GetSystemMenu($confirmationHandle, $false)
      $closeState = [uint32]::MaxValue
      if ($menu -ne [IntPtr]::Zero) { $closeState = [NativeInput]::GetMenuState($menu, 0xF060, 0) }
      if ($request.choice -eq 'close') {
        if (![NativeInput]::PostMessage($confirmationHandle, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero)) { throw 'Confirmation WM_CLOSE failed' }
      } else {
        Focus-Control (Find-Control '^No$' 'Button')
        [System.Windows.Forms.SendKeys]::SendWait('{ESC}')
      }
      $deadline = (Get-Date).AddSeconds(1)
      while ([NativeInput]::IsWindowVisible($confirmationHandle) -and (Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 50
      }
      if ([NativeInput]::IsWindowVisible($confirmationHandle)) {
        $focused = [System.Windows.Automation.AutomationElement]::FocusedElement.Current
        $response = @{ confirmation = 'remained-open'; choice = $request.choice; closeMenuState = $closeState; focusedName = $focused.Name; focusedType = $focused.ControlType.ProgrammaticName }
      } else {
        $response.confirmation = 'dismissed'
        Focus-Control (Find-Control '^Save file$' 'Window')
        [System.Windows.Forms.SendKeys]::SendWait('{ESC}')
      }
    } else { throw 'Unsupported replacement choice' }
  }
  'menu-reload' {
    $view = Find-Control '^View$' 'MenuItem'
    $view.GetCurrentPattern([System.Windows.Automation.ExpandCollapsePattern]::Pattern).Expand()
    Click-Control (Find-Control '^Reload$' 'MenuItem')
  }
  default { throw 'Unsupported native UI command' }
}
$response | ConvertTo-Json -Compress
