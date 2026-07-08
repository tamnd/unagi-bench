# unagi-bench workload
# tier: 1
# tag: collections
# desc: build a homogeneous list and reduce it, the list-strategy lowering target

def run(n):
    xs = []
    i = 0
    while i < n:
        xs.append(i * 2)
        i += 1
    total = 0
    for v in xs:
        total += v
    return total


print(run(2000000))
