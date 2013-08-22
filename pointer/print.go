package pointer

import "fmt"

func (c *addrConstraint) String() string {
	return fmt.Sprintf("addr n%d <- {&n%d}", c.dst, c.src)
}

func (c *copyConstraint) String() string {
	return fmt.Sprintf("copy n%d <- n%d", c.dst, c.src)
}

func (c *loadConstraint) String() string {
	return fmt.Sprintf("load n%d <- n%d[%d]", c.dst, c.src, c.offset)
}

func (c *storeConstraint) String() string {
	return fmt.Sprintf("store n%d[%d] <- n%d", c.dst, c.offset, c.src)
}

func (c *offsetAddrConstraint) String() string {
	return fmt.Sprintf("offsetAddr n%d <- n%d.#%d", c.dst, c.src, c.offset)
}

func (c *typeAssertConstraint) String() string {
	return fmt.Sprintf("typeAssert n%d <- n%d.(%s)", c.dst, c.src, c.typ)
}

func (c *invokeConstraint) String() string {
	return fmt.Sprintf("invoke n%d.%s(n%d ...)", c.iface, c.method.Name(), c.params+1)
}

func (n nodeid) String() string {
	return fmt.Sprintf("n%d", n)
}
