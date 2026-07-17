module greeting_mod
  implicit none
contains
  function greet(name) result(msg)
    character(len=*), intent(in) :: name
    character(len=100) :: msg
    msg = "Hello, " // name
  end function greet

  subroutine announce(name)
    character(len=*), intent(in) :: name
    print *, greet(name)
  end subroutine announce
end module greeting_mod
