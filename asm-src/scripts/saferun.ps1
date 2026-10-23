# Run a program under a memory cap and a time limit.
#
# Why this exists: nothing in a scandiacus-built program collects garbage
# on its own, so a loop that allocates and never exits eats every byte
# of RAM and pagefile on the machine. That is not a crash, it is a hard
# freeze that needs the power button. It has happened.
#
# Use this for any program built from library code that has not been run
# before, especially anything with a while loop in it.
#
#   .\saferun.ps1 -Exe .\prog.exe
#   .\saferun.ps1 -Exe .\prog.exe -MemoryMB 256 -TimeoutSec 20
#
# The cap is a job object limit, so an allocation past it fails inside
# the child. The rest of the machine is untouched.

param(
    [Parameter(Mandatory = $true)][string]$Exe,
    [string[]]$Arguments = @(),
    [int]$MemoryMB = 512,
    [int]$TimeoutSec = 30
)

$ErrorActionPreference = 'Stop'

Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

public static class JobCap {
    [StructLayout(LayoutKind.Sequential)]
    struct BasicLimits {
        public long PerProcessUserTimeLimit;
        public long PerJobUserTimeLimit;
        public uint LimitFlags;
        public IntPtr MinimumWorkingSetSize;
        public IntPtr MaximumWorkingSetSize;
        public uint ActiveProcessLimit;
        public IntPtr Affinity;
        public uint PriorityClass;
        public uint SchedulingClass;
    }

    [StructLayout(LayoutKind.Sequential)]
    struct IoCounters {
        public ulong ReadOperationCount;
        public ulong WriteOperationCount;
        public ulong OtherOperationCount;
        public ulong ReadTransferCount;
        public ulong WriteTransferCount;
        public ulong OtherTransferCount;
    }

    [StructLayout(LayoutKind.Sequential)]
    struct ExtendedLimits {
        public BasicLimits BasicLimitInformation;
        public IoCounters IoInfo;
        public IntPtr ProcessMemoryLimit;
        public IntPtr JobMemoryLimit;
        public IntPtr PeakProcessMemoryUsed;
        public IntPtr PeakJobMemoryUsed;
    }

    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern IntPtr CreateJobObject(IntPtr a, string name);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool SetInformationJobObject(IntPtr job, int infoClass, IntPtr info, uint len);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool TerminateJobObject(IntPtr job, uint exitCode);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool CloseHandle(IntPtr h);

    const uint LIMIT_PROCESS_MEMORY = 0x00000100;
    const uint LIMIT_JOB_MEMORY = 0x00000200;
    const uint LIMIT_KILL_ON_JOB_CLOSE = 0x00002000;
    const int ExtendedLimitInformation = 9;

    public static IntPtr Create(long bytes) {
        IntPtr job = CreateJobObject(IntPtr.Zero, null);
        if (job == IntPtr.Zero) throw new Exception("CreateJobObject failed");

        ExtendedLimits info = new ExtendedLimits();
        info.BasicLimitInformation.LimitFlags =
            LIMIT_PROCESS_MEMORY | LIMIT_JOB_MEMORY | LIMIT_KILL_ON_JOB_CLOSE;
        info.ProcessMemoryLimit = (IntPtr)bytes;
        info.JobMemoryLimit = (IntPtr)bytes;

        uint len = (uint)Marshal.SizeOf(typeof(ExtendedLimits));
        IntPtr buf = Marshal.AllocHGlobal((int)len);
        try {
            Marshal.StructureToPtr(info, buf, false);
            if (!SetInformationJobObject(job, ExtendedLimitInformation, buf, len))
                throw new Exception("SetInformationJobObject failed: " + Marshal.GetLastWin32Error());
        } finally {
            Marshal.FreeHGlobal(buf);
        }
        return job;
    }

    public static void Assign(IntPtr job, IntPtr process) {
        if (!AssignProcessToJobObject(job, process))
            throw new Exception("AssignProcessToJobObject failed: " + Marshal.GetLastWin32Error());
    }

    public static void Kill(IntPtr job) { TerminateJobObject(job, 1); }
    public static void Close(IntPtr job) { CloseHandle(job); }
}
'@

$exePath = (Resolve-Path $Exe).Path
$job = [JobCap]::Create([long]$MemoryMB * 1MB)

$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = $exePath
$psi.Arguments = ($Arguments -join ' ')
$psi.UseShellExecute = $false

$proc = [System.Diagnostics.Process]::Start($psi)

# assign straight away. the child needs a few ms to get going, so the
# window where it is running uncapped is not big enough to matter.
[JobCap]::Assign($job, $proc.Handle)

if (-not $proc.WaitForExit($TimeoutSec * 1000)) {
    Write-Host "saferun: timed out after $TimeoutSec s, killing" -ForegroundColor Yellow
    [JobCap]::Kill($job)
    $proc.WaitForExit(5000)
    [JobCap]::Close($job)
    exit 124
}

$code = $proc.ExitCode
[JobCap]::Close($job)

if ($code -ne 0) {
    Write-Host "saferun: exit $code" -ForegroundColor Yellow
}
exit $code
