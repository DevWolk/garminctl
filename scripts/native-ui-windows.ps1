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
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window, uint message, IntPtr wparam, IntPtr lparam);
}
'@
$request = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RequestBase64)) | ConvertFrom-Json
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
          if ($current.Name -match $Name -and (!$Kind -or $current.ControlType.ProgrammaticName -eq "ControlType.$Kind") -and !$current.IsOffscreen) { return $element }
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
  [void][NativeInput]::SetForegroundWindow([IntPtr]$element.Current.NativeWindowHandle)
  $element.SetFocus()
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
    $inputBox.GetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern).SetValue([string]$request.path)
    $inputBox.SetFocus()
    [System.Windows.Forms.SendKeys]::SendWait('{ENTER}')
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
      if ($request.choice -eq 'close') {
        [void][NativeInput]::PostMessage([IntPtr]$dialog.Current.NativeWindowHandle, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero)
      } else { [System.Windows.Forms.SendKeys]::SendWait('{ESC}') }
      Focus-Control (Find-Control '^Save file$' 'Window')
      [System.Windows.Forms.SendKeys]::SendWait('{ESC}')
    } else { throw 'Unsupported replacement choice' }
  }
  'menu-reload' {
    $view = Find-Control '^View$' 'MenuItem'
    $view.GetCurrentPattern([System.Windows.Automation.ExpandCollapsePattern]::Pattern).Expand()
    Click-Control (Find-Control '^Reload$' 'MenuItem')
  }
  default { throw 'Unsupported native UI command' }
}
'{}'
