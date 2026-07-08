# unagi-bench workload
# tier: 2
# tag: recursion
# desc: naive recursive fibonacci, a static-to-static call storm

def fib(n):
    if n < 2:
        return n
    return fib(n - 1) + fib(n - 2)


print(fib(32))
