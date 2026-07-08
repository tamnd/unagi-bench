# unagi-bench workload
# tier: 1
# tag: numeric
# desc: float sum of squares, the canonical static-tier numeric loop

def sumsq(n):
    total = 0.0
    i = 0
    while i < n:
        x = float(i)
        total += x * x
        i += 1
    return total


print(sumsq(3000000))
