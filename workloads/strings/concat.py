# unagi-bench workload
# tier: 3
# tag: strings
# desc: repeated string building, the read-only-string concat path

def run(n):
    s = ""
    i = 0
    while i < n:
        s += "ab"
        i += 1
    return len(s)


print(run(400000))
