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
    [StructLayout(LayoutKind.Sequential)]
    private struct MouseInput {
        public int dx, dy;
        public uint mouseData, flags, time;
        public UIntPtr extra;
    }
    [StructLayout(LayoutKind.Sequential)]
    private struct MouseEvent {
        public uint type;
        public MouseInput data;
    }
    [DllImport("user32.dll", SetLastError = true)]
    private static extern uint SendInput(uint count, MouseEvent[] inputs, int size);
    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool SetCursorPos(int x, int y);
    private static MouseEvent ButtonEvent(uint flags) {
        return new MouseEvent { type = 0, data = new MouseInput {
            dx = 0, dy = 0, mouseData = 0, flags = flags, time = 0, extra = UIntPtr.Zero
        }};
    }
    public static uint Click(int x, int y) {
        if (!SetCursorPos(x, y)) { throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error()); }
        MouseEvent[] inputs = { ButtonEvent(0x0002), ButtonEvent(0x0004) };
        uint sent = SendInput(2, inputs, Marshal.SizeOf(typeof(MouseEvent)));
        if (sent != 2) { throw new InvalidOperationException("Native click injected " + sent + " of 2 events; Win32 error " + Marshal.GetLastWin32Error()); }
        return sent;
    }
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
      # A dialog can close between the window listing and the walk of its controls.
      try { $elements = @($window) + @($window.FindAll($scope, $all)) }
      catch [System.Windows.Automation.ElementNotAvailableException] { continue }
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
    try { $elements = @($window) + @($window.FindAll($scope, $all)) }
    catch [System.Windows.Automation.ElementNotAvailableException] { continue }
    foreach ($element in $elements) {
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

function Click-MenuControl($element) {
  $point = $element.GetClickablePoint()
  return [NativeInput]::Click([int]$point.X, [int]$point.Y)
}

switch ($request.action) {
  'application-process' {
    $deadline = (Get-Date).AddSeconds(15)
    do {
      $found = @{}
      foreach ($window in [System.Windows.Automation.AutomationElement]::RootElement.FindAll([System.Windows.Automation.TreeScope]::Children, $all)) {
        try {
          $current = $window.Current
          if ($current.Name -ne 'garminctl' -or $current.ProcessId -eq $request.previous) { continue }
          $process = Get-Process -Id $current.ProcessId -ErrorAction Stop
          if ($process.Path -ieq $request.path) { $found[$process.Id] = $true }
        } catch [System.Windows.Automation.ElementNotAvailableException] { }
      }
      if ($found.Count -gt 1) { throw 'Multiple restarted garminctl windows.' }
      if ($found.Count -eq 1) { $response.pid = @($found.Keys)[0]; break }
      Start-Sleep -Milliseconds 100
    } while ((Get-Date) -lt $deadline)
    if (!$response.pid) { throw 'Restarted native window not found for the installed desktop path.' }
  }
  'application-closed' {
    $deadline = (Get-Date).AddSeconds(15)
    while (Get-Process -Id $AppProcessId -ErrorAction SilentlyContinue) {
      if ((Get-Date) -ge $deadline) { throw 'Restarted native process did not close.' }
      Start-Sleep -Milliseconds 100
    }
  }
  'activate' {
    Click-Control (Find-Control $request.pattern 'Button')
  }
  'toggle' {
    $element = Find-Control $request.pattern 'CheckBox'
    $toggle = $element.GetCurrentPattern([System.Windows.Automation.TogglePattern]::Pattern)
    $expected = [System.Windows.Automation.ToggleState]::Off
    if ($request.checked) { $expected = [System.Windows.Automation.ToggleState]::On }
    if ($toggle.Current.ToggleState -ne $expected) { $toggle.Toggle() }
    if ($toggle.Current.ToggleState -ne $expected) { throw 'Native checkbox state did not match the requested selection.' }
  }
  'text' {
    $response.text = (Find-Control $request.pattern).Current.Name
  }
  'reveal' {
    Focus-Control (Find-Control '^garminctl$' 'Window')
    [void](Click-MenuControl (Find-Control $request.pattern))
  }
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
    Focus-Control (Find-Control '^garminctl$' 'Window')
    [Console]::Error.WriteLine('native-menu-stage: window-focused')
    $view = Find-Control '^View$' 'MenuItem'
    [Console]::Error.WriteLine('native-menu-stage: view-found')
    $viewEvents = Click-MenuControl $view
    [Console]::Error.WriteLine('native-menu-stage: view-clicked')
    $reload = Find-Control '^Reload(?:\s+F5)?$' 'MenuItem'
    [Console]::Error.WriteLine('native-menu-stage: reload-found')
    $reloadEvents = Click-MenuControl $reload
    [Console]::Error.WriteLine('native-menu-stage: reload-clicked')
    $response.menuEvents = @($viewEvents, $reloadEvents)
  }
  default { throw 'Unsupported native UI command' }
}
$response | ConvertTo-Json -Compress
