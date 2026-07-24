import sys

path = sys.argv[1]
s = open(path).read()
old = "\t\tif (!is_intel || !is_pvm_feature_control_msr_valid(pvm, msr_info))\n\t\t\treturn 1;\n\t\tpvm->msr_ia32_feature_control = data;"
new = "\t\tif (!msr_info->host_initiated &&\n\t\t    (!is_intel || !is_pvm_feature_control_msr_valid(pvm, msr_info)))\n\t\t\treturn 1;\n\t\tpvm->msr_ia32_feature_control = data;"
if new.split("\n")[0] in s:
    print("already patched")
elif old in s:
    open(path, "w").write(s.replace(old, new))
    print("patched FEAT_CTL host_initiated")
else:
    print("PATTERN NOT FOUND")
    sys.exit(1)
