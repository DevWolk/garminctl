# garminctl WPD bridge: a JSON-lines server over stdin/stdout that talks to
# MTP devices through the Windows Portable Devices COM API.
$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = [System.Text.Encoding]::UTF8
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

$src = @'
using System;
using System.Collections.Generic;
using System.IO;
using System.Runtime.InteropServices;
using System.Runtime.InteropServices.ComTypes;

namespace GarminCtl
{
    [StructLayout(LayoutKind.Sequential, Pack = 4)]
    public struct PropertyKey
    {
        public Guid fmtid;
        public uint pid;
        public PropertyKey(string g, uint p) { fmtid = new Guid(g); pid = p; }
    }

    [StructLayout(LayoutKind.Sequential)]
    public struct PropVariant
    {
        public ushort vt;
        public ushort r1, r2, r3;
        public IntPtr p;
        public IntPtr p2;
    }

    [ComImport, Guid("a1567595-4c2f-4574-a6fa-ecef917b9a40"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    public interface IPortableDeviceManager
    {
        void GetDevices(IntPtr ids, ref uint count);
        void RefreshDeviceList();
        void GetDeviceFriendlyName([MarshalAs(UnmanagedType.LPWStr)] string id, IntPtr name, ref uint len);
        void GetDeviceDescription([MarshalAs(UnmanagedType.LPWStr)] string id, IntPtr name, ref uint len);
        void GetDeviceManufacturer([MarshalAs(UnmanagedType.LPWStr)] string id, IntPtr name, ref uint len);
        void GetDeviceProperty();
        void GetPrivateDevices();
    }

    [ComImport, Guid("625e2df8-6392-4cf0-9ad1-3cfa5f17775c"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    public interface IPortableDevice
    {
        void Open([MarshalAs(UnmanagedType.LPWStr)] string id, IPortableDeviceValues clientInfo);
        void SendCommand(uint flags, IPortableDeviceValues parameters, out IPortableDeviceValues results);
        void Content(out IPortableDeviceContent content);
        void Capabilities();
        void Cancel();
        void Close();
        void Advise();
        void Unadvise();
        void GetPnPDeviceID();
    }

    [ComImport, Guid("6a96ed84-7c73-4480-9938-bf5af477d426"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    public interface IPortableDeviceContent
    {
        void EnumObjects(uint flags, [MarshalAs(UnmanagedType.LPWStr)] string parent, IPortableDeviceValues filter, out IEnumPortableDeviceObjectIDs e);
        void Properties(out IPortableDeviceProperties props);
        void Transfer(out IPortableDeviceResources res);
        void CreateObjectWithPropertiesOnly(IPortableDeviceValues values, [MarshalAs(UnmanagedType.LPWStr)] ref string id);
        void CreateObjectWithPropertiesAndData(IPortableDeviceValues values, out IStream stream, ref uint optimalSize, IntPtr cookie);
        void Delete(uint options, IPortableDevicePropVariantCollection ids, IntPtr results);
        void GetObjectIDsFromPersistentUniqueIDs();
        void Cancel();
        void Move();
        void Copy();
    }

    [ComImport, Guid("10ece955-cf41-4728-bfa0-41eedf1bbf19"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    public interface IEnumPortableDeviceObjectIDs
    {
        [PreserveSig]
        int Next(uint count, [Out, MarshalAs(UnmanagedType.LPArray, ArraySubType = UnmanagedType.LPWStr, SizeParamIndex = 0)] string[] ids, ref uint fetched);
        void Skip(uint count);
        void Reset();
        void Clone();
    }

    [ComImport, Guid("7f6d695c-03df-4439-a809-59266beee3a6"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    public interface IPortableDeviceProperties
    {
        void GetSupportedProperties([MarshalAs(UnmanagedType.LPWStr)] string id, out IPortableDeviceKeyCollection keys);
        void GetPropertyAttributes();
        void GetValues([MarshalAs(UnmanagedType.LPWStr)] string id, IPortableDeviceKeyCollection keys, out IPortableDeviceValues values);
        void SetValues([MarshalAs(UnmanagedType.LPWStr)] string id, IPortableDeviceValues values, out IPortableDeviceValues results);
        void Delete();
        void Cancel();
    }

    [ComImport, Guid("fd8878ac-d841-4d17-891c-e6829cdb6934"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    public interface IPortableDeviceResources
    {
        void GetSupportedResources();
        void GetResourceAttributes();
        void GetStream([MarshalAs(UnmanagedType.LPWStr)] string id, ref PropertyKey resource, uint mode, ref uint optimalSize, out IStream stream);
        void Delete();
        void Cancel();
        void CreateResource();
    }

    [ComImport, Guid("dada2357-e0ad-492e-98db-dd61c53ba353"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    public interface IPortableDeviceKeyCollection
    {
        void GetCount(ref uint count);
        void GetAt(uint index, ref PropertyKey key);
        void Add(ref PropertyKey key);
        void Clear();
        void RemoveAt(uint index);
    }

    [ComImport, Guid("89b2e422-4f1b-4316-bcef-a44afea83eb3"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    public interface IPortableDevicePropVariantCollection
    {
        void GetCount(ref uint count);
        void GetAt(uint index, ref PropVariant value);
        void Add(ref PropVariant value);
        void GetType(out ushort type);
        void ChangeType(ushort type);
        void Clear();
        void RemoveAt(uint index);
    }

    [ComImport, Guid("6848f6f2-3155-4f86-b6f5-263eeeab3143"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    public interface IPortableDeviceValues
    {
        void GetCount(ref uint count);
        void GetAt(uint index, ref PropertyKey key, ref PropVariant value);
        void SetValue(ref PropertyKey key, ref PropVariant value);
        void GetValue(ref PropertyKey key, out PropVariant value);
        void SetStringValue(ref PropertyKey key, [MarshalAs(UnmanagedType.LPWStr)] string value);
        void GetStringValue(ref PropertyKey key, [MarshalAs(UnmanagedType.LPWStr)] out string value);
        void SetUnsignedIntegerValue(ref PropertyKey key, uint value);
        void GetUnsignedIntegerValue(ref PropertyKey key, out uint value);
        void SetSignedIntegerValue(ref PropertyKey key, int value);
        void GetSignedIntegerValue(ref PropertyKey key, out int value);
        void SetUnsignedLargeIntegerValue(ref PropertyKey key, ulong value);
        void GetUnsignedLargeIntegerValue(ref PropertyKey key, out ulong value);
        void SetSignedLargeIntegerValue(ref PropertyKey key, long value);
        void GetSignedLargeIntegerValue(ref PropertyKey key, out long value);
        void SetFloatValue(ref PropertyKey key, float value);
        void GetFloatValue(ref PropertyKey key, out float value);
        void SetErrorValue(ref PropertyKey key, int value);
        void GetErrorValue(ref PropertyKey key, out int value);
        void SetKeyValue(ref PropertyKey key, ref PropertyKey value);
        void GetKeyValue(ref PropertyKey key, out PropertyKey value);
        void SetBoolValue(ref PropertyKey key, int value);
        void GetBoolValue(ref PropertyKey key, out int value);
        void SetIUnknownValue(ref PropertyKey key, [MarshalAs(UnmanagedType.IUnknown)] object value);
        void GetIUnknownValue(ref PropertyKey key, [MarshalAs(UnmanagedType.IUnknown)] out object value);
        void SetGuidValue(ref PropertyKey key, ref Guid value);
        void GetGuidValue(ref PropertyKey key, out Guid value);
        void SetBufferValue(ref PropertyKey key, IntPtr value, uint len);
        void GetBufferValue(ref PropertyKey key, out IntPtr value, out uint len);
        void SetIPortableDeviceValuesValue(ref PropertyKey key, IPortableDeviceValues value);
        void GetIPortableDeviceValuesValue(ref PropertyKey key, out IPortableDeviceValues value);
        void SetIPortableDevicePropVariantCollectionValue(ref PropertyKey key, IPortableDevicePropVariantCollection value);
        void GetIPortableDevicePropVariantCollectionValue(ref PropertyKey key, out IPortableDevicePropVariantCollection value);
        void SetIPortableDeviceKeyCollectionValue(ref PropertyKey key, IPortableDeviceKeyCollection value);
        void GetIPortableDeviceKeyCollectionValue(ref PropertyKey key, out IPortableDeviceKeyCollection value);
        void SetIPortableDeviceValuesCollectionValue();
        void GetIPortableDeviceValuesCollectionValue();
        void RemoveValue(ref PropertyKey key);
        void CopyValuesFromPropertyStore();
        void CopyValuesToPropertyStore();
        void Clear();
    }

    public class Entry
    {
        public string Name;
        public string Id;
        public bool Dir;
        public ulong Size;
        public string Modified;
    }

    public class DeviceInfo
    {
        public string Id;
        public string Name;
        public string Manufacturer;
        public string Description;
    }

    public class Wpd
    {
        const string ObjProps = "EF6B490D-5CD8-437A-AFFC-DA8B60EE4A3C";
        static PropertyKey WPD_OBJECT_ID = new PropertyKey(ObjProps, 2);
        static PropertyKey WPD_OBJECT_PARENT_ID = new PropertyKey(ObjProps, 3);
        static PropertyKey WPD_OBJECT_NAME = new PropertyKey(ObjProps, 4);
        static PropertyKey WPD_OBJECT_FORMAT = new PropertyKey(ObjProps, 6);
        static PropertyKey WPD_OBJECT_CONTENT_TYPE = new PropertyKey(ObjProps, 7);
        static PropertyKey WPD_OBJECT_SIZE = new PropertyKey(ObjProps, 11);
        static PropertyKey WPD_OBJECT_ORIGINAL_FILE_NAME = new PropertyKey(ObjProps, 12);
        static PropertyKey WPD_OBJECT_DATE_MODIFIED = new PropertyKey(ObjProps, 19);
        const string ClientProps = "204D9F0C-2292-4080-9F42-40664E70F859";
        static PropertyKey WPD_CLIENT_NAME = new PropertyKey(ClientProps, 2);
        static PropertyKey WPD_CLIENT_MAJOR_VERSION = new PropertyKey(ClientProps, 3);
        static PropertyKey WPD_CLIENT_MINOR_VERSION = new PropertyKey(ClientProps, 4);
        static PropertyKey WPD_CLIENT_REVISION = new PropertyKey(ClientProps, 5);
        static PropertyKey WPD_RESOURCE_DEFAULT = new PropertyKey("E81E79BE-34F0-41BF-B53F-F1A06AE87842", 0);
        static Guid CT_FOLDER = new Guid("27E2E392-A111-48E0-AB0C-E17705A05F85");
        static Guid CT_FUNCTIONAL = new Guid("99ED0160-17FF-4C44-9D98-1D7A6F941921");
        static Guid CT_GENERIC_FILE = new Guid("0085E0A6-8D34-45D7-BC5C-447E59C73D48");
        static Guid FORMAT_UNSPECIFIED = new Guid("30000000-AE6C-4804-98BA-C57B46965FE7");

        IPortableDevice device;
        IPortableDeviceContent content;
        IPortableDeviceProperties props;
        IPortableDeviceKeyCollection listKeys;
        string storageId;
        Dictionary<string, string> dirCache = new Dictionary<string, string>();

        static object Create(string clsid)
        {
            return Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid(clsid)));
        }

        public string StorageId { get { return storageId; } }

        // Raw MTP operations through WPD's MTP extension commands (WpdMtpExtensions.h).
        // Needed for Garmin's vendor operation 0x9002, which lists the app folder that
        // ordinary enumeration may hide.
        const string MtpExt = "4D545058-1A2E-4106-A357-771E0819FC56";
        const string CatCommon = "F0422A9C-5DC8-4440-B5BD-5DF28835658A";
        static PropertyKey WPD_PROPERTY_COMMON_COMMAND_CATEGORY = new PropertyKey(CatCommon, 1001);
        static PropertyKey WPD_PROPERTY_COMMON_COMMAND_ID = new PropertyKey(CatCommon, 1002);
        static PropertyKey WPD_PROPERTY_COMMON_HRESULT = new PropertyKey(CatCommon, 1003);
        static PropertyKey MTP_OPERATION_CODE = new PropertyKey(MtpExt, 1001);
        static PropertyKey MTP_OPERATION_PARAMS = new PropertyKey(MtpExt, 1002);
        static PropertyKey MTP_RESPONSE_CODE = new PropertyKey(MtpExt, 1003);
        static PropertyKey MTP_RESPONSE_PARAMS = new PropertyKey(MtpExt, 1004);
        static PropertyKey MTP_TRANSFER_CONTEXT = new PropertyKey(MtpExt, 1006);
        static PropertyKey MTP_TRANSFER_TOTAL_DATA_SIZE = new PropertyKey(MtpExt, 1007);
        static PropertyKey MTP_TRANSFER_NUM_BYTES_TO_READ = new PropertyKey(MtpExt, 1008);
        static PropertyKey MTP_TRANSFER_NUM_BYTES_READ = new PropertyKey(MtpExt, 1009);
        static PropertyKey MTP_TRANSFER_NUM_BYTES_TO_WRITE = new PropertyKey(MtpExt, 1010);
        static PropertyKey MTP_TRANSFER_NUM_BYTES_WRITTEN = new PropertyKey(MtpExt, 1011);
        static PropertyKey MTP_TRANSFER_DATA = new PropertyKey(MtpExt, 1012);
        static PropertyKey MTP_OPTIMAL_TRANSFER_BUFFER_SIZE = new PropertyKey(MtpExt, 1013);

        IPortableDeviceValues NewCommand(uint commandId)
        {
            var v = (IPortableDeviceValues)Create("0c15d503-d017-47ce-9016-7b3f978721cc");
            var cat = new Guid(MtpExt);
            v.SetGuidValue(ref WPD_PROPERTY_COMMON_COMMAND_CATEGORY, ref cat);
            v.SetUnsignedIntegerValue(ref WPD_PROPERTY_COMMON_COMMAND_ID, commandId);
            return v;
        }

        IPortableDeviceValues SendCommand(IPortableDeviceValues p)
        {
            IPortableDeviceValues r;
            device.SendCommand(0, p, out r);
            int hr = 0;
            try { r.GetErrorValue(ref WPD_PROPERTY_COMMON_HRESULT, out hr); } catch { }
            if (hr < 0) Marshal.ThrowExceptionForHR(hr);
            return r;
        }

        IPortableDevicePropVariantCollection U32Collection(uint[] vals)
        {
            var coll = (IPortableDevicePropVariantCollection)Create("08a99e2f-6d6d-4b80-af5a-baf2bcbe4cb9");
            foreach (var x in vals) { var pv = new PropVariant(); pv.vt = 19; pv.p = (IntPtr)(long)x; coll.Add(ref pv); }
            return coll;
        }

        uint[] ResponseParams(IPortableDeviceValues r)
        {
            try
            {
                IPortableDevicePropVariantCollection pc;
                r.GetIPortableDevicePropVariantCollectionValue(ref MTP_RESPONSE_PARAMS, out pc);
                uint n = 0; pc.GetCount(ref n);
                var arr = new uint[n];
                for (uint i = 0; i < n; i++) { var pv = new PropVariant(); pc.GetAt(i, ref pv); arr[i] = (uint)pv.p.ToInt64(); }
                return arr;
            }
            catch { return new uint[0]; }
        }

        // MtpRead runs one MTP operation whose data phase comes from the device.
        public byte[] MtpRead(uint opcode, uint[] prms, out uint code, out uint[] rparams)
        {
            var p = NewCommand(13);
            p.SetUnsignedIntegerValue(ref MTP_OPERATION_CODE, opcode);
            p.SetIPortableDevicePropVariantCollectionValue(ref MTP_OPERATION_PARAMS, U32Collection(prms));
            var r = SendCommand(p);
            string ctx; r.GetStringValue(ref MTP_TRANSFER_CONTEXT, out ctx);
            ulong total = 0; try { r.GetUnsignedLargeIntegerValue(ref MTP_TRANSFER_TOTAL_DATA_SIZE, out total); } catch { }
            uint optimal = 0; try { r.GetUnsignedIntegerValue(ref MTP_OPTIMAL_TRANSFER_BUFFER_SIZE, out optimal); } catch { }
            if (optimal == 0) optimal = 256 * 1024;
            bool unknown = total == 0 || total == 0xFFFFFFFF;
            var ms = new System.IO.MemoryStream();
            while (unknown || (ulong)ms.Length < total)
            {
                uint want = unknown ? optimal : (uint)Math.Min((ulong)optimal, total - (ulong)ms.Length);
                var rp = NewCommand(15);
                rp.SetStringValue(ref MTP_TRANSFER_CONTEXT, ctx);
                rp.SetUnsignedIntegerValue(ref MTP_TRANSFER_NUM_BYTES_TO_READ, want);
                IntPtr buf = Marshal.AllocCoTaskMem((int)want);
                try
                {
                    rp.SetBufferValue(ref MTP_TRANSFER_DATA, buf, want);
                    var rr = SendCommand(rp);
                    uint got = 0; rr.GetUnsignedIntegerValue(ref MTP_TRANSFER_NUM_BYTES_READ, out got);
                    IntPtr data; uint len; rr.GetBufferValue(ref MTP_TRANSFER_DATA, out data, out len);
                    if (got > len) got = len;
                    var chunk = new byte[got]; Marshal.Copy(data, chunk, 0, (int)got);
                    ms.Write(chunk, 0, chunk.Length);
                    if (got < want) break;
                }
                finally { Marshal.FreeCoTaskMem(buf); }
            }
            var ep = NewCommand(17);
            ep.SetStringValue(ref MTP_TRANSFER_CONTEXT, ctx);
            var er = SendCommand(ep);
            code = 0; try { er.GetUnsignedIntegerValue(ref MTP_RESPONSE_CODE, out code); } catch { }
            rparams = ResponseParams(er);
            return ms.ToArray();
        }

        // MtpWrite runs one MTP operation whose data phase goes to the device.
        public void MtpWrite(uint opcode, uint[] prms, byte[] data, out uint code, out uint[] rparams)
        {
            var p = NewCommand(14);
            p.SetUnsignedIntegerValue(ref MTP_OPERATION_CODE, opcode);
            p.SetIPortableDevicePropVariantCollectionValue(ref MTP_OPERATION_PARAMS, U32Collection(prms));
            p.SetUnsignedLargeIntegerValue(ref MTP_TRANSFER_TOTAL_DATA_SIZE, (ulong)data.Length);
            IPortableDeviceValues r;
            try { r = SendCommand(p); } catch (Exception e) { throw new Exception("execute 0x" + opcode.ToString("X4") + " with data to write: " + e.Message, e); }
            string ctx;
            try { r.GetStringValue(ref MTP_TRANSFER_CONTEXT, out ctx); } catch (Exception e) { throw new Exception("execute 0x" + opcode.ToString("X4") + ": no transfer context (" + DescribeValues(r) + "): " + e.Message, e); }
            uint optimal = 0; try { r.GetUnsignedIntegerValue(ref MTP_OPTIMAL_TRANSFER_BUFFER_SIZE, out optimal); } catch { }
            if (optimal == 0) optimal = 256 * 1024;
            int off = 0;
            while (off < data.Length)
            {
                uint n = (uint)Math.Min((long)optimal, data.Length - off);
                var wp = NewCommand(16);
                wp.SetStringValue(ref MTP_TRANSFER_CONTEXT, ctx);
                wp.SetUnsignedIntegerValue(ref MTP_TRANSFER_NUM_BYTES_TO_WRITE, n);
                IntPtr buf = Marshal.AllocCoTaskMem((int)n);
                try
                {
                    Marshal.Copy(data, off, buf, (int)n);
                    wp.SetBufferValue(ref MTP_TRANSFER_DATA, buf, n);
                    IPortableDeviceValues wr;
                    try { wr = SendCommand(wp); } catch (Exception e) { throw new Exception("write data at " + off + ": " + e.Message, e); }
                    uint written = n; try { wr.GetUnsignedIntegerValue(ref MTP_TRANSFER_NUM_BYTES_WRITTEN, out written); } catch { }
                    if (written == 0) throw new Exception("MTP write stalled");
                    off += (int)written;
                }
                finally { Marshal.FreeCoTaskMem(buf); }
            }
            var ep = NewCommand(17);
            ep.SetStringValue(ref MTP_TRANSFER_CONTEXT, ctx);
            IPortableDeviceValues er;
            try { er = SendCommand(ep); } catch (Exception e) { throw new Exception("end transfer: " + e.Message, e); }
            code = 0; try { er.GetUnsignedIntegerValue(ref MTP_RESPONSE_CODE, out code); } catch { }
            rparams = ResponseParams(er);
        }

        // DescribeValues lists the keys a result set carries, for error messages.
        static string DescribeValues(IPortableDeviceValues v)
        {
            var sb = new System.Text.StringBuilder();
            uint n = 0; v.GetCount(ref n);
            for (uint i = 0; i < n; i++)
            {
                var k = new PropertyKey(); var pv = new PropVariant();
                v.GetAt(i, ref k, ref pv);
                sb.Append(k.pid).Append(":vt").Append(pv.vt);
                if (pv.vt == 19 || pv.vt == 3 || pv.vt == 10) sb.Append("=0x").Append(((uint)pv.p.ToInt64()).ToString("X8"));
                sb.Append(' ');
            }
            return sb.ToString().Trim();
        }

        // MtpNoData runs one MTP operation without a data phase.
        public void MtpNoData(uint opcode, uint[] prms, out uint code, out uint[] rparams)
        {
            var p = NewCommand(12);
            p.SetUnsignedIntegerValue(ref MTP_OPERATION_CODE, opcode);
            p.SetIPortableDevicePropVariantCollectionValue(ref MTP_OPERATION_PARAMS, U32Collection(prms));
            var r = SendCommand(p);
            code = 0; try { r.GetUnsignedIntegerValue(ref MTP_RESPONSE_CODE, out code); } catch { }
            rparams = ResponseParams(r);
        }

        static string GetName(IPortableDeviceManager mgr, string id, int which)
        {
            uint len = 0;
            if (which == 0) mgr.GetDeviceFriendlyName(id, IntPtr.Zero, ref len);
            else if (which == 1) mgr.GetDeviceManufacturer(id, IntPtr.Zero, ref len);
            else mgr.GetDeviceDescription(id, IntPtr.Zero, ref len);
            if (len == 0) return "";
            IntPtr buf = Marshal.AllocCoTaskMem((int)len * 2);
            try
            {
                if (which == 0) mgr.GetDeviceFriendlyName(id, buf, ref len);
                else if (which == 1) mgr.GetDeviceManufacturer(id, buf, ref len);
                else mgr.GetDeviceDescription(id, buf, ref len);
                return Marshal.PtrToStringUni(buf).TrimEnd('\0');
            }
            finally { Marshal.FreeCoTaskMem(buf); }
        }

        public static List<DeviceInfo> Devices()
        {
            var mgr = (IPortableDeviceManager)Create("0af10cec-2ecd-4b92-9581-34f6ae0637f3");
            mgr.RefreshDeviceList();
            uint count = 0;
            mgr.GetDevices(IntPtr.Zero, ref count);
            var list = new List<DeviceInfo>();
            if (count == 0) return list;
            IntPtr arr = Marshal.AllocCoTaskMem(IntPtr.Size * (int)count);
            try
            {
                mgr.GetDevices(arr, ref count);
                for (int i = 0; i < count; i++)
                {
                    IntPtr p = Marshal.ReadIntPtr(arr, i * IntPtr.Size);
                    string id = Marshal.PtrToStringUni(p);
                    Marshal.FreeCoTaskMem(p);
                    var d = new DeviceInfo();
                    d.Id = id;
                    try { d.Name = GetName(mgr, id, 0); } catch { d.Name = ""; }
                    try { d.Manufacturer = GetName(mgr, id, 1); } catch { d.Manufacturer = ""; }
                    try { d.Description = GetName(mgr, id, 2); } catch { d.Description = ""; }
                    list.Add(d);
                }
            }
            finally { Marshal.FreeCoTaskMem(arr); }
            return list;
        }

        public void Open(string id)
        {
            var info = (IPortableDeviceValues)Create("0c15d503-d017-47ce-9016-7b3f978721cc");
            info.SetStringValue(ref WPD_CLIENT_NAME, "garminctl");
            info.SetUnsignedIntegerValue(ref WPD_CLIENT_MAJOR_VERSION, 1);
            info.SetUnsignedIntegerValue(ref WPD_CLIENT_MINOR_VERSION, 0);
            info.SetUnsignedIntegerValue(ref WPD_CLIENT_REVISION, 0);
            device = (IPortableDevice)Create("f7c0039a-4762-488a-b4b3-760ef9a1ba9b");
            device.Open(id, info);
            device.Content(out content);
            content.Properties(out props);
            listKeys = (IPortableDeviceKeyCollection)Create("de2d022d-2480-43be-97f0-d1fa2cf98f4f");
            listKeys.Add(ref WPD_OBJECT_NAME);
            listKeys.Add(ref WPD_OBJECT_ORIGINAL_FILE_NAME);
            listKeys.Add(ref WPD_OBJECT_CONTENT_TYPE);
            listKeys.Add(ref WPD_OBJECT_SIZE);
            listKeys.Add(ref WPD_OBJECT_DATE_MODIFIED);
            storageId = null;
            dirCache.Clear();
            foreach (var e in Children("DEVICE"))
            {
                if (storageId == null || e.Dir) { storageId = e.Id; if (e.Dir) break; }
            }
            if (storageId == null) throw new Exception("device has no storage");
        }

        public void Close()
        {
            if (device != null) { try { device.Close(); } catch { } }
            device = null; content = null; props = null; dirCache.Clear();
        }

        public List<Entry> Children(string parentId)
        {
            var list = new List<Entry>();
            IEnumPortableDeviceObjectIDs en;
            content.EnumObjects(0, parentId, null, out en);
            var ids = new string[64];
            while (true)
            {
                uint fetched = 0;
                int hr = en.Next((uint)ids.Length, ids, ref fetched);
                if (hr < 0) Marshal.ThrowExceptionForHR(hr);
                for (int i = 0; i < fetched; i++) list.Add(Describe(ids[i]));
                if (fetched < ids.Length) break;
            }
            return list;
        }

        Entry Describe(string id)
        {
            var e = new Entry();
            e.Id = id;
            IPortableDeviceValues v;
            props.GetValues(id, listKeys, out v);
            string name = null, orig = null;
            try { v.GetStringValue(ref WPD_OBJECT_ORIGINAL_FILE_NAME, out orig); } catch { }
            try { v.GetStringValue(ref WPD_OBJECT_NAME, out name); } catch { }
            e.Name = !string.IsNullOrEmpty(orig) ? orig : (name ?? id);
            Guid ct = Guid.Empty;
            try { v.GetGuidValue(ref WPD_OBJECT_CONTENT_TYPE, out ct); } catch { }
            e.Dir = ct == CT_FOLDER || ct == CT_FUNCTIONAL;
            if (!e.Dir)
            {
                try { ulong sz; v.GetUnsignedLargeIntegerValue(ref WPD_OBJECT_SIZE, out sz); e.Size = sz; } catch { }
            }
            try
            {
                PropVariant pv;
                v.GetValue(ref WPD_OBJECT_DATE_MODIFIED, out pv);
                if (pv.vt == 7)
                {
                    double d = BitConverter.Int64BitsToDouble(pv.p.ToInt64());
                    e.Modified = DateTime.FromOADate(d).ToString("yyyy-MM-ddTHH:mm:ss");
                }
            }
            catch { }
            Marshal.ReleaseComObject(v);
            return e;
        }

        static string[] Split(string path)
        {
            return path.Replace('\\', '/').Trim('/').Split(new char[] { '/' }, StringSplitOptions.RemoveEmptyEntries);
        }

        // ResolveDir returns the object id of a directory path relative to the storage root.
        public string ResolveDir(string path)
        {
            var parts = Split(path);
            string key = string.Join("/", parts).ToLowerInvariant();
            string cached;
            if (dirCache.TryGetValue(key, out cached)) return cached;
            string id = storageId;
            string sofar = "";
            foreach (var part in parts)
            {
                sofar = sofar == "" ? part.ToLowerInvariant() : sofar + "/" + part.ToLowerInvariant();
                string hit;
                if (dirCache.TryGetValue(sofar, out hit)) { id = hit; continue; }
                Entry found = null;
                foreach (var e in Children(id))
                {
                    if (e.Dir && string.Equals(e.Name, part, StringComparison.OrdinalIgnoreCase)) { found = e; break; }
                }
                if (found == null) throw new FileNotFoundException("directory not found: " + path);
                dirCache[sofar] = found.Id;
                id = found.Id;
            }
            return id;
        }

        public Entry Find(string path)
        {
            var parts = Split(path);
            if (parts.Length == 0) return null;
            string dir = string.Join("/", parts, 0, parts.Length - 1);
            string dirId = ResolveDir(dir);
            foreach (var e in Children(dirId))
            {
                if (string.Equals(e.Name, parts[parts.Length - 1], StringComparison.OrdinalIgnoreCase)) return e;
            }
            return null;
        }

        public List<Entry> List(string path)
        {
            return Children(ResolveDir(path));
        }

        public byte[] Read(string path)
        {
            var e = Find(path);
            if (e == null) throw new FileNotFoundException("not found: " + path);
            IPortableDeviceResources res;
            content.Transfer(out res);
            uint optimal = 0;
            IStream stream;
            res.GetStream(e.Id, ref WPD_RESOURCE_DEFAULT, 0, ref optimal, out stream);
            if (optimal == 0) optimal = 65536;
            var ms = new MemoryStream();
            var buf = new byte[optimal];
            IntPtr pRead = Marshal.AllocCoTaskMem(4);
            try
            {
                while (true)
                {
                    stream.Read(buf, buf.Length, pRead);
                    int n = Marshal.ReadInt32(pRead);
                    if (n <= 0) break;
                    ms.Write(buf, 0, n);
                }
            }
            finally
            {
                Marshal.FreeCoTaskMem(pRead);
                Marshal.ReleaseComObject(stream);
                Marshal.ReleaseComObject(res);
            }
            return ms.ToArray();
        }

        public void Delete(string path)
        {
            var e = Find(path);
            if (e == null) throw new FileNotFoundException("not found: " + path);
            DeleteId(e.Id);
            var parts = Split(path);
            dirCache.Remove(string.Join("/", parts).ToLowerInvariant());
        }

        void DeleteId(string id)
        {
            var coll = (IPortableDevicePropVariantCollection)Create("08a99e2f-6d6d-4b80-af5a-baf2bcbe4cb9");
            var pv = new PropVariant();
            pv.vt = 31;
            pv.p = Marshal.StringToCoTaskMemUni(id);
            try { coll.Add(ref pv); content.Delete(0, coll, IntPtr.Zero); }
            finally { Marshal.FreeCoTaskMem(pv.p); Marshal.ReleaseComObject(coll); }
        }

        public string Mkdir(string path)
        {
            var parts = Split(path);
            string parent = ResolveDir(string.Join("/", parts, 0, parts.Length - 1));
            string name = parts[parts.Length - 1];
            var values = (IPortableDeviceValues)Create("0c15d503-d017-47ce-9016-7b3f978721cc");
            values.SetStringValue(ref WPD_OBJECT_PARENT_ID, parent);
            values.SetStringValue(ref WPD_OBJECT_NAME, name);
            values.SetStringValue(ref WPD_OBJECT_ORIGINAL_FILE_NAME, name);
            values.SetGuidValue(ref WPD_OBJECT_CONTENT_TYPE, ref CT_FOLDER);
            string id = null;
            content.CreateObjectWithPropertiesOnly(values, ref id);
            Marshal.ReleaseComObject(values);
            return id;
        }

        public void Write(string path, byte[] data)
        {
            var parts = Split(path);
            string parent = ResolveDir(string.Join("/", parts, 0, parts.Length - 1));
            string name = parts[parts.Length - 1];
            foreach (var e in Children(parent))
            {
                if (!e.Dir && string.Equals(e.Name, name, StringComparison.OrdinalIgnoreCase)) DeleteId(e.Id);
            }
            var values = (IPortableDeviceValues)Create("0c15d503-d017-47ce-9016-7b3f978721cc");
            values.SetStringValue(ref WPD_OBJECT_PARENT_ID, parent);
            values.SetUnsignedLargeIntegerValue(ref WPD_OBJECT_SIZE, (ulong)data.Length);
            values.SetStringValue(ref WPD_OBJECT_ORIGINAL_FILE_NAME, name);
            values.SetStringValue(ref WPD_OBJECT_NAME, name);
            values.SetGuidValue(ref WPD_OBJECT_CONTENT_TYPE, ref CT_GENERIC_FILE);
            values.SetGuidValue(ref WPD_OBJECT_FORMAT, ref FORMAT_UNSPECIFIED);
            IStream stream;
            uint optimal = 0;
            content.CreateObjectWithPropertiesAndData(values, out stream, ref optimal, IntPtr.Zero);
            if (optimal == 0) optimal = 65536;
            IntPtr pWritten = Marshal.AllocCoTaskMem(4);
            try
            {
                int off = 0;
                while (off < data.Length)
                {
                    int n = Math.Min((int)optimal, data.Length - off);
                    var chunk = new byte[n];
                    Array.Copy(data, off, chunk, 0, n);
                    stream.Write(chunk, n, pWritten);
                    off += n;
                }
                stream.Commit(0);
            }
            finally
            {
                Marshal.FreeCoTaskMem(pWritten);
                Marshal.ReleaseComObject(stream);
                Marshal.ReleaseComObject(values);
            }
        }
    }
}
'@

Add-Type -TypeDefinition $src -Language CSharp
$wpd = New-Object GarminCtl.Wpd

function Reply($obj) {
    $json = $obj | ConvertTo-Json -Compress -Depth 8
    [Console]::Out.WriteLine($json)
    [Console]::Out.Flush()
}

Reply @{ ok = $true; ready = $true }

while ($true) {
    $line = [Console]::In.ReadLine()
    if ($line -eq $null) { break }
    if ($line.Trim() -eq '') { continue }
    try {
        $req = $line | ConvertFrom-Json
        $resp = @{ id = $req.id; ok = $true }
        switch ($req.op) {
            'devices' { $resp.devices = @([GarminCtl.Wpd]::Devices() | ForEach-Object { @{ id = $_.Id; name = $_.Name; manufacturer = $_.Manufacturer; description = $_.Description } }) }
            'open'    { $wpd.Open($req.device) }
            'close'   { $wpd.Close() }
            'list'    { $resp.entries = @($wpd.List($req.path) | ForEach-Object { @{ name = $_.Name; dir = $_.Dir; size = $_.Size; modified = $_.Modified } }) }
            'stat'    { $e = $wpd.Find($req.path); if ($e -eq $null) { $resp.ok = $false; $resp.error = 'not found' } else { $resp.entry = @{ name = $e.Name; dir = $e.Dir; size = $e.Size; modified = $e.Modified } } }
            'read'    { $resp.b64 = [Convert]::ToBase64String($wpd.Read($req.path)) }
            'write'   { $wpd.Write($req.path, [Convert]::FromBase64String($req.b64)) }
            'delete'  { $wpd.Delete($req.path) }
            'mkdir'   { $wpd.Mkdir($req.path) | Out-Null }
            'listid'  { $resp.entries = @($wpd.Children($req.objectId) | ForEach-Object { @{ name = $_.Name; dir = $_.Dir; size = $_.Size; modified = $_.Modified } }) }
            'objectid' { $e = $wpd.Find($req.path); if ($e -eq $null) { $resp.ok = $false; $resp.error = 'not found' } else { $resp.objectId = $e.Id; $resp.storage = $wpd.StorageId } }
            'mtpread' { [uint32]$code = 0; [uint32[]]$rp = @(); $data = $wpd.MtpRead([uint32]$req.opcode, [uint32[]]@($req.params), [ref]$code, [ref]$rp); $resp.b64 = [Convert]::ToBase64String($data); $resp.code = $code; $resp.params = @($rp) }
            'mtpwrite' { [uint32]$code = 0; [uint32[]]$rp = @(); $wpd.MtpWrite([uint32]$req.opcode, [uint32[]]@($req.params), [Convert]::FromBase64String($req.b64), [ref]$code, [ref]$rp); $resp.code = $code; $resp.params = @($rp) }
            'mtpnodata' { [uint32]$code = 0; [uint32[]]$rp = @(); $wpd.MtpNoData([uint32]$req.opcode, [uint32[]]@($req.params), [ref]$code, [ref]$rp); $resp.code = $code; $resp.params = @($rp) }
            'quit'    { Reply @{ id = $req.id; ok = $true }; break }
            default   { $resp.ok = $false; $resp.error = ('unknown op ' + $req.op) }
        }
        Reply $resp
    } catch {
        $msg = $_.Exception.Message
        if ($_.Exception.InnerException) { $msg = $msg + ': ' + $_.Exception.InnerException.Message }
        Reply @{ id = $req.id; ok = $false; error = $msg }
    }
}
