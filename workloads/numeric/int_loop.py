# unagi-bench workload
# tier: 1
# tag: numeric
# desc: integer accumulation with a modulo mix, exercises the overflow-guarded int path

def run(n):
    acc = 0
    i = 1
    while i < n:
        acc += i * 3 - (i % 7)
        i += 1
    return acc


print(run(3000000))
